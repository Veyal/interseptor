package store

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func r2SearchIDs(t *testing.T, s *Store, term string) map[int64]bool {
	t.Helper()
	flows, err := s.QueryFlowsFilter(FlowFilter{Search: term, Limit: 100})
	if err != nil {
		t.Fatalf("search %q: %v", term, err)
	}
	out := map[int64]bool{}
	for _, f := range flows {
		out[f.ID] = true
	}
	return out
}

// The full-text index used to be backfilled only when it was completely empty.
// An index that lost some rows (a crash mid-backfill, rows written while FTS
// was unavailable) or kept rows of deleted flows stayed wrong forever: History
// search silently missed flows. Opening the store must bring it back in step.
func TestR2OpenRepairsADriftedFullTextIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var ids []int64
	for i := 0; i < 6; i++ {
		ids = append(ids, reviewFlow(t, s, Flow{TS: time.UnixMilli(int64(1000 + i)), Scheme: "https", Host: "api.example.com", Path: "/orders/needle", Status: 200}))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err := sql.Open("sqlite", storeDSN(filepath.Join(dir, currentDBName)))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	// Two indexed rows go missing (one in the middle, the newest one), and one
	// index entry outlives its flow.
	for _, q := range []string{
		`DELETE FROM flows_fts WHERE rowid IN (?, ?)`,
	} {
		if _, err := db.Exec(q, ids[2], ids[5]); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM flows WHERE id = ?`, ids[0]); err != nil {
		t.Fatalf("orphan the index entry: %v", err)
	}
	db.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	got := r2SearchIDs(t, s, "needle")
	for _, id := range ids[1:] {
		if !got[id] {
			t.Errorf("flow %d is missing from full-text search after reopen", id)
		}
	}
	if len(got) != 5 {
		t.Errorf("search returned %d flows, want 5", len(got))
	}
	var indexed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows_fts`).Scan(&indexed); err != nil || indexed != 5 {
		t.Errorf("index holds %d entries (%v), want 5: the entry of the deleted flow must go", indexed, err)
	}
}

// An index that is in step must be left alone: no rewrite on every open.
func TestR2OpenLeavesAnInStepFullTextIndexAlone(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	reviewFlow(t, s, Flow{Scheme: "https", Host: "api.example.com", Path: "/needle", Status: 200})
	if drift, err := s.flowsFTSDrifted(); err != nil || drift {
		t.Fatalf("flowsFTSDrifted on a healthy index = %v, %v", drift, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if got := r2SearchIDs(t, s, "needle"); len(got) != 1 {
		t.Fatalf("search returned %d flows, want 1", len(got))
	}
}

func r2WriteBody(t *testing.T, s *Store, data string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatalf("NewBodyWriter: %v", err)
	}
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sum, n, err := w.Finalize()
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Finalize: n=%d err=%v", n, err)
	}
	return sum
}

// Body files are renamed into place without an fsync, so after a power loss a
// content-addressed file can exist with no (or short) contents. Every later
// capture of the same body used to deduplicate onto that damaged file and keep
// it damaged; a stored file whose length is not the content's length is not
// that content and must be replaced.
func TestR2DamagedBodyFileIsReplacedByTheNextIdenticalBody(t *testing.T) {
	for name, damage := range map[string]int64{"empty": 0, "short": 5} {
		t.Run(name, func(t *testing.T) {
			s := reviewOpen(t)
			const body = "the quick brown fox jumps over the lazy dog"
			sum := r2WriteBody(t, s, body)
			if err := os.Truncate(s.bodyPath(sum), damage); err != nil {
				t.Fatalf("truncate: %v", err)
			}
			if again := r2WriteBody(t, s, body); again != sum {
				t.Fatalf("hash changed: %s vs %s", again, sum)
			}
			rc, err := s.OpenBody(sum)
			if err != nil {
				t.Fatalf("OpenBody: %v", err)
			}
			got, _ := io.ReadAll(rc)
			rc.Close()
			if string(got) != body {
				t.Fatalf("stored body = %q, want the full content back", got)
			}
			if tmps, _ := filepath.Glob(filepath.Join(s.bodiesDir, ".tmp-*")); len(tmps) != 0 {
				t.Errorf("temp files left behind: %v", tmps)
			}
		})
	}
}
