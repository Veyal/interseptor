package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// pragmaBenchFlows is the history size the pragma measurements run against —
// a realistic single-engagement capture, big enough that the page cache, the
// mmap window and the sort scratch space all matter.
const pragmaBenchFlows = 40000

// buildPragmaBenchDB fills one project database with pragmaBenchFlows flows and
// returns the directory holding it. The database is built once per benchmark
// binary and reused by every pragma variant, so they all read identical bytes.
func buildPragmaBenchDB(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	s, err := Open(dir)
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	hosts := []string{"api.example.com", "cdn.example.com", "auth.example.com", "shop.example.com", "www.example.net"}
	mimes := []string{"application/json", "text/html", "image/png", "text/javascript", "text/css"}
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	for i := 0; i < pragmaBenchFlows; i++ {
		f := &Flow{
			TS:          time.UnixMilli(int64(1_700_000_000_000 + i*37)),
			Method:      methods[i%len(methods)],
			Scheme:      "https",
			Host:        hosts[i%len(hosts)],
			Port:        443,
			Path:        fmt.Sprintf("/v2/%s/%d/items?page=%d", []string{"orders", "users", "carts", "assets"}[i%4], i%977, i%50),
			HTTPVersion: "HTTP/1.1",
			Status:      []int{200, 201, 204, 301, 400, 401, 403, 404, 500}[i%9],
			ReqLen:      int64(120 + i%900),
			ResLen:      int64(400 + (i*7)%90000),
			Mime:        mimes[i%len(mimes)],
			DurationMs:  int64(3 + i%400),
			ClientAddr:  "127.0.0.1:" + strconv.Itoa(40000+i%2000),
			Note:        map[bool]string{true: "", false: "checked for idor on tenant parameter"}[i%11 != 0],
		}
		if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
			tb.Fatalf("enqueue %d: ok=%v err=%v", i, ok, err)
		}
		if (i+1)%1024 == 0 {
			if err := s.FlushFlows(); err != nil {
				tb.Fatalf("FlushFlows: %v", err)
			}
		}
	}
	if err := s.Close(); err != nil {
		tb.Fatalf("Close: %v", err)
	}
	// Fold the WAL back into the main database so every variant starts from the
	// same on-disk shape.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, currentDBName))
	if err != nil {
		tb.Fatalf("checkpoint open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		tb.Fatalf("checkpoint: %v", err)
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		tb.Fatalf("analyze: %v", err)
	}
	db.Close()
	return dir
}

// openWithPragmas opens an existing project database with an explicit pragma
// list, for comparing settings against each other.
func openWithPragmas(tb testing.TB, dir string, pragmas []string) *Store {
	tb.Helper()
	var b strings.Builder
	b.WriteString("file:")
	b.WriteString(filepath.Join(dir, currentDBName))
	b.WriteString("?_txlock=immediate")
	for _, p := range pragmas {
		b.WriteString("&_pragma=")
		b.WriteString(p)
	}
	db, err := sql.Open("sqlite", b.String())
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	// A bare Store is enough for read benchmarks: the write-behind queue is
	// empty, so syncFlows is a no-op and no flusher goroutine is needed.
	return &Store{db: db, bodiesDir: filepath.Join(dir, "bodies")}
}

// pragmaVariants are the settings compared by BenchmarkStorePragmas. "before"
// is what the store shipped with; the rest add one setting at a time so each
// one has to earn its place.
func pragmaVariants() []struct {
	name    string
	pragmas []string
} {
	base := []string{
		"busy_timeout(10000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(1)",
	}
	with := func(extra ...string) []string {
		out := append([]string(nil), base...)
		return append(out, extra...)
	}
	return []struct {
		name    string
		pragmas []string
	}{
		{"before", base},
		{"cache32", with("cache_size(-32000)")},
		{"cache64", with("cache_size(-64000)")},
		{"mmap256", with("mmap_size(268435456)")},
		{"tempmem", with("temp_store(MEMORY)")},
		{"cache32mem", with("cache_size(-32000)", "temp_store(MEMORY)")},
		{"issue101", with("cache_size(-64000)", "mmap_size(268435456)", "temp_store(MEMORY)")},
		{"shipped", storePragmas},
	}
}

// BenchmarkStorePragmas times the queries the History panel and search run on a
// large capture under each pragma variant. Read it per sub-benchmark name: the
// numbers are only meaningful against "before" on the same machine and the same
// database.
func BenchmarkStorePragmas(b *testing.B) {
	dir := buildPragmaBenchDB(b)
	for _, v := range pragmaVariants() {
		b.Run(v.name, func(b *testing.B) {
			s := openWithPragmas(b, dir, v.pragmas)
			defer s.db.Close()
			// One warm pass so the comparison is steady-state, not first-touch.
			if _, err := s.QueryFlowsListFilter(FlowFilter{Limit: 200, SortKey: "size", SortDir: -1}); err != nil {
				b.Fatalf("warm: %v", err)
			}

			b.Run("list-by-size", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.QueryFlowsListFilter(FlowFilter{Limit: 200, SortKey: "size", SortDir: -1}); err != nil {
						b.Fatalf("query: %v", err)
					}
				}
			})
			b.Run("list-by-path", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.QueryFlowsListFilter(FlowFilter{Limit: 200, SortKey: "path", SortDir: 1}); err != nil {
						b.Fatalf("query: %v", err)
					}
				}
			})
			b.Run("search", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.QueryFlowsListFilter(FlowFilter{Limit: 200, Search: "orders"}); err != nil {
						b.Fatalf("query: %v", err)
					}
				}
			})
			b.Run("host-stats", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.HostStats(); err != nil {
						b.Fatalf("host stats: %v", err)
					}
				}
			})
			// GCBodies' reference query is a four-way UNION, which SQLite answers
			// with four temp B-trees — the one read path in the store that really
			// does spill, and the reason temp_store is set.
			b.Run("gc-refs", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.referencedBodyHashes(); err != nil {
						b.Fatalf("gc refs: %v", err)
					}
				}
			})
		})
	}
}

// BenchmarkFlowDrainPragmas measures the write-behind flusher under each pragma
// variant: the sub-journal that temp_store moves off disk is on the write path
// too, not just the read path.
func BenchmarkFlowDrainPragmas(b *testing.B) {
	for _, v := range pragmaVariants() {
		b.Run(v.name, func(b *testing.B) {
			dir := b.TempDir()
			// Build the schema through the real Open, then reopen with the
			// variant's pragmas.
			seed, err := Open(dir)
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			if err := seed.Close(); err != nil {
				b.Fatalf("Close: %v", err)
			}
			s := openWithPragmas(b, dir, v.pragmas)
			defer s.db.Close()
			s.flowPendingN = map[int64]int{}
			s.flowVoid = map[int64]struct{}{}
			s.flowWake = make(chan struct{}, 1)
			s.flowStop = make(chan struct{})
			s.flowPaused = true

			const batch = flowBatchMax
			b.ResetTimer()
			for i := 0; i < b.N; i += batch {
				n := batch
				if i+n > b.N {
					n = b.N - i
				}
				b.StopTimer()
				for j := 0; j < n; j++ {
					if ok, err := s.EnqueueInsertFlow(benchFlow(i + j)); err != nil || !ok {
						b.Fatalf("enqueue: ok=%v err=%v", ok, err)
					}
				}
				b.StartTimer()
				if err := s.drainOnce(); err != nil {
					b.Fatalf("drainOnce: %v", err)
				}
			}
		})
	}
}

// TestStorePragmasApplied proves the DSN pragmas actually reach a pooled
// connection. A pragma the driver silently ignores (mmap when the build has it
// compiled out, say) would otherwise look like a setting we rely on.
func TestStorePragmasApplied(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	for _, want := range []struct {
		pragma string
		value  string
	}{
		{"cache_size", "-32000"},
		{"journal_mode", "wal"},
		{"busy_timeout", "10000"},
	} {
		var got string
		if err := s.db.QueryRow(`PRAGMA ` + want.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", want.pragma, err)
		}
		if !strings.EqualFold(got, want.value) {
			t.Fatalf("PRAGMA %s = %q, want %q", want.pragma, got, want.value)
		}
	}
}
