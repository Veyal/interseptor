package store

import "testing"

// AI activity is persisted per-project so it survives restarts; ListActivity
// returns it newest-first, bounded by limit.
func TestActivityStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if got, err := s.ListActivity(50); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %d (err %v)", len(got), err)
	}

	for i := int64(1); i <= 3; i++ {
		id, err := s.InsertActivity(&Activity{TS: i, Tool: "list_flows", Summary: "n=2", OK: true, Result: "ok", Ms: 5})
		if err != nil || id == 0 {
			t.Fatalf("insert %d: id=%d err=%v", i, id, err)
		}
	}

	got, err := s.ListActivity(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0].ID <= got[2].ID {
		t.Fatalf("not newest-first: %d then %d", got[0].ID, got[2].ID)
	}
	if got[0].Tool != "list_flows" || !got[0].OK {
		t.Fatalf("round-trip wrong: %+v", got[0])
	}
	if lim, _ := s.ListActivity(2); len(lim) != 2 {
		t.Fatalf("limit: got %d, want 2", len(lim))
	}
}

// DeleteActivity wipes the feed — a persisted clear, so it stays empty on reload.
func TestDeleteActivity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for i := int64(1); i <= 3; i++ {
		if _, err := s.InsertActivity(&Activity{TS: i, Tool: "x", OK: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteActivity(); err != nil {
		t.Fatalf("DeleteActivity: %v", err)
	}
	if got, _ := s.ListActivity(50); len(got) != 0 {
		t.Fatalf("after clear: got %d, want 0", len(got))
	}
}

// InsertActivity rolls its insert back when retention fails, so an error cannot
// leave an unannounced row behind or let the bounded table grow.
func TestInsertActivityPropagatesPruneError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := activityKeep
	activityKeep = 1
	defer func() { activityKeep = old }()

	// Seed rows so a later insert's retention DELETE (id <= id-activityKeep) has
	// something to remove, then block that DELETE with a trigger.
	for i := 0; i < 3; i++ {
		if _, err := s.InsertActivity(&Activity{TS: int64(i), Tool: "x", OK: true}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if _, err := s.db.Exec(
		`CREATE TRIGGER activity_no_delete BEFORE DELETE ON activity
		 BEGIN SELECT RAISE(FAIL, 'prune blocked'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	a := &Activity{TS: 99, Tool: "x", OK: true}
	id, err := s.InsertActivity(a)
	if err == nil {
		t.Fatal("expected retention DELETE error to propagate, got nil")
	}
	if id != 0 || a.ID != 0 {
		t.Fatalf("failed insert published ids return=%d struct=%d", id, a.ID)
	}
	if got, err := s.ListActivity(50); err != nil || len(got) != 1 {
		t.Fatalf("rows after failed bounded insert = %d, err=%v; want 1", len(got), err)
	}
}
