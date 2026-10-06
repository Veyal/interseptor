package store

import (
	"fmt"
	"strings"
	"testing"
)

func runRec(ts int64) []byte {
	return []byte(fmt.Sprintf(`{"runId":"r","startedTs":%d,"finishedTs":%d,"attack":"repeat","target":"example.com"}`, ts, ts+5))
}

func TestIntruderRunsTableIdempotentOnOldDB(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a database created before the table existed.
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS intruder_runs`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.ListIntruderRuns(10); len(got) != i {
			t.Fatalf("reopen %d: list len %d", i, len(got))
		}
		if err := s.PutIntruderRun(fmt.Sprintf("run-%d", i), runRec(int64(100+i))); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestIntruderRunsPutGetListPrune(t *testing.T) {
	s := newTestStore(t)
	for i := 1; i <= 25; i++ {
		if err := s.PutIntruderRun(fmt.Sprintf("run-%02d", i), runRec(int64(1000+i))); err != nil {
			t.Fatal(err)
		}
	}
	list := s.ListIntruderRuns(0)
	if len(list) != 20 {
		t.Fatalf("kept %d runs, want 20", len(list))
	}
	if list[0].RunID != "run-25" || list[19].RunID != "run-06" || list[0].Attack != "repeat" || list[0].FinishedTS != 1030 {
		t.Fatalf("order/meta: first=%+v last=%+v", list[0], list[19])
	}
	if _, ok, _ := s.GetIntruderRun("run-05"); ok {
		t.Fatal("pruned run still present")
	}
	rec, ok, err := s.GetIntruderRun("run-25")
	if err != nil || !ok || !strings.Contains(string(rec), "example.com") {
		t.Fatalf("get: %q %v %v", rec, ok, err)
	}
	if got := s.ListIntruderRuns(3); len(got) != 3 {
		t.Fatalf("limit: %d", len(got))
	}
	// Re-put replaces rather than duplicating.
	if err := s.PutIntruderRun("run-25", runRec(2000)); err != nil || len(s.ListIntruderRuns(0)) != 20 {
		t.Fatalf("replace: %v", err)
	}
}

func TestIntruderRunsRejectsOversizedAndBadIDs(t *testing.T) {
	s := newTestStore(t)
	if err := s.PutIntruderRun("big", make([]byte, maxIntruderRunBytes+1)); err != ErrIntruderRunTooLarge {
		t.Fatalf("oversize: %v", err)
	}
	for _, id := range []string{"", "Bad ID", "a/b", strings.Repeat("a", 81)} {
		if err := s.PutIntruderRun(id, runRec(1)); err == nil {
			t.Fatalf("id %q accepted", id)
		}
	}
	if _, ok, err := s.GetIntruderRun("missing"); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
}
