package store

import (
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// r3MeasureN reads a row count for the opt-in measurement tests. They are
// skipped unless the variable is set, so the ordinary suite stays fast.
func r3MeasureN(t *testing.T, env string) int {
	t.Helper()
	n, err := strconv.Atoi(os.Getenv(env))
	if err != nil || n <= 0 {
		t.Skipf("set %s=<rows> to run this measurement", env)
	}
	return n
}

// r3Measure runs fn while sampling the heap, and reports wall time, bytes
// allocated and the peak of HeapInuse.
func r3Measure(t *testing.T, name string, fn func()) {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapInuse)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()
	start := time.Now()
	fn()
	elapsed := time.Since(start)
	close(stop)
	<-done
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapInuse > peak.Load() {
		peak.Store(after.HeapInuse)
	}
	const mb = 1 << 20
	t.Logf("%s: %v, allocated %.1f MB in %d objects, heap in use %.1f MB before, %.1f MB peak",
		name, elapsed.Round(time.Millisecond), float64(after.TotalAlloc-before.TotalAlloc)/mb,
		after.Mallocs-before.Mallocs, float64(before.HeapInuse)/mb, float64(peak.Load())/mb)
}

// TestR3MeasurePruneUndo prunes every flow of a seeded project and undoes it.
func TestR3MeasurePruneUndo(t *testing.T) {
	n := r3MeasureN(t, "ISP_R3_PRUNE_FLOWS")
	s := seedReviewDB(t, n)
	var res PruneResult
	r3Measure(t, "prune "+strconv.Itoa(n), func() {
		var err error
		if res, err = s.PruneFlows(PruneOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if res.Deleted != int64(n) {
		t.Fatalf("deleted %d, want %d", res.Deleted, n)
	}
	var cell, chunk, chunks int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(length(CAST(flows AS BLOB))),0) FROM prune_snapshots`).Scan(&cell)
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(length(CAST(flows AS BLOB))),0), COUNT(*) FROM prune_snapshot_chunks`).Scan(&chunk, &chunks)
	t.Logf("largest cell: prune_snapshots.flows %.2f MB, prune_snapshot_chunks.flows %.2f MB (%d chunks)",
		float64(cell)/(1<<20), float64(chunk)/(1<<20), chunks)
	r3Measure(t, "PendingPrunes", func() {
		if p, err := s.PendingPrunes(); err != nil || len(p) != 1 || p[0].Flows != n {
			t.Fatalf("pending = %+v, %v", p, err)
		}
	})
	r3Measure(t, "undo "+strconv.Itoa(n), func() {
		got, err := s.RestorePrune(res.PruneID)
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(n) {
			t.Fatalf("restored %d, want %d", got, n)
		}
	})
}

// TestR3MeasureBulkTags tags and untags every flow of a seeded project, with
// the per-flow implementation the bulk method replaced (r3ReferenceMutateTags)
// and with MutateFlowTagsBulk, each on its own fresh database.
func TestR3MeasureBulkTags(t *testing.T) {
	n := r3MeasureN(t, "ISP_R3_TAG_FLOWS")
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	run := func(name string, mutate func(s *Store, add, remove []string) error) {
		s := seedReviewDB(t, n)
		for _, step := range []struct {
			what        string
			add, remove []string
		}{
			{"add 1 tag", []string{"reviewed"}, nil},
			{"add 2 tags", []string{"triaged", "auth"}, nil},
			{"same again (no change)", []string{"triaged", "auth"}, nil},
			{"remove 1 tag", nil, []string{"reviewed"}},
		} {
			start := time.Now()
			if err := mutate(s, step.add, step.remove); err != nil {
				t.Fatal(err)
			}
			t.Logf("%-9s %7d flows  %-24s %v", name, n, step.what, time.Since(start).Round(100*time.Microsecond))
		}
	}
	run("per-flow", func(s *Store, add, remove []string) error { return r3ReferenceMutateTags(s, ids, add, remove) })
	run("bulk", func(s *Store, add, remove []string) error {
		_, err := s.MutateFlowTagsBulk(ids, add, remove)
		return err
	})
}

// TestR3SeedScratchDB writes a seeded project of ISP_R3_SEED_FLOWS flows, 20
// of them TLS failures, to the directory ISP_R3_SEED_DIR for the measurements
// that reuse one large database.
func TestR3SeedScratchDB(t *testing.T) {
	n := r3MeasureN(t, "ISP_R3_SEED_FLOWS")
	dir := os.Getenv("ISP_R3_SEED_DIR")
	if dir == "" {
		t.Skip("set ISP_R3_SEED_DIR")
	}
	s := seedReviewDB(t, n)
	if _, err := s.db.Exec(`UPDATE flows SET flags = flags | ?, status = 0 WHERE id % 5000 = 0`, FlagTLSFailed); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, dir+"/"+currentDBName); err != nil {
		t.Fatal(err)
	}
}

// r3OpenScratch opens the database ISP_R3_DB_DIR points at (a copy the caller
// owns), for the measurements that need a large capture.
func r3OpenScratch(t *testing.T) *Store {
	t.Helper()
	dir := os.Getenv("ISP_R3_DB_DIR")
	if dir == "" {
		t.Skip("set ISP_R3_DB_DIR to a seeded project directory")
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// r3Time runs a query five times, reading every row, and returns the fastest
// run and the row count.
func r3Time(t *testing.T, s *Store, q string, args ...any) (time.Duration, int) {
	t.Helper()
	best := time.Hour
	n := 0
	for i := 0; i < 5; i++ {
		start := time.Now()
		rows, err := s.db.Query(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		n = 0
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best, n
}

// TestR3MeasureTLSHost times the host-filtered TLS diagnosis reads on a large
// scratch database: the plain "walk the success index and test the host" SQL
// against the store methods.
func TestR3MeasureTLSHost(t *testing.T) {
	s := r3OpenScratch(t)
	// A pinned host: TLS failures only.
	if _, err := s.db.Exec(`UPDATE flows SET host = 'pinned.example.org' WHERE (flags & 16) != 0 AND id % 10000 = 0`); err != nil {
		t.Fatal(err)
	}
	best := func(fn func()) time.Duration {
		d := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			fn()
			if e := time.Since(start); e < d {
				d = e
			}
		}
		return d
	}
	for _, host := range []string{"nosuchhost", "pinned", "svc07", "example"} {
		oldCount, _ := r3Time(t, s, `SELECT COUNT(*) FROM (SELECT 1`+httpsOKFrom+httpsOKPredicate+httpsOKHostClause+` LIMIT ?)`, host, 10000)
		oldNewest, _ := r3Time(t, s, newestHTTPSOKSQL+httpsOKHostClause, host)
		var n int64
		newCount := best(func() { n, _ = s.CountSuccessfulHTTPSUpTo(host, 10000) })
		newStats := best(func() { _, _, _, _ = s.TLSFailureStats(host, 1) })
		t.Logf("host=%-10s capped count: %12v -> %12v (%d)   newest success: %12v -> %12v (whole TLSFailureStats)", host, oldCount, newCount, n, oldNewest, newStats)
	}
}
