package store

import (
	"testing"
	"time"
)

// DeleteFlows removes the given flow rows and reports how many were deleted;
// an empty id list is a no-op.
func TestDeleteFlows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	n, err := s.DeleteFlows(ids[:2])
	if err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}
	got, err := s.QueryFlowsFilter(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ids[2] {
		t.Fatalf("after delete: got %d flows, want only the third", len(got))
	}
	if n, _ := s.DeleteFlows(nil); n != 0 {
		t.Fatalf("empty delete should be a no-op, got n=%d", n)
	}
}

// DeleteFlows must remove the flow's full-text-search row in the same transaction,
// so a deleted flow can never linger in the FTS index (which would surface it in
// search or, on rowid reuse, mismatch a different flow).
func TestDeleteFlowsClearsFTSIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "uniquehost.example", Path: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	ftsCount := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows_fts WHERE rowid=?`, id).Scan(&n); err != nil {
			t.Fatalf("count fts: %v", err)
		}
		return n
	}
	if ftsCount() != 1 {
		t.Fatalf("expected 1 FTS row before delete, got %d", ftsCount())
	}
	if _, err := s.DeleteFlows([]int64{id}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}
	if ftsCount() != 0 {
		t.Fatalf("FTS row not cleaned after delete (orphan would surface in search)")
	}
}

// DeleteFlows must chunk its id statements: a bulk delete of a full capture
// session (the API accepts up to 100k ids) would otherwise build one giant
// IN (?,?,…) with more host parameters than SQLite's 32766 limit and fail the
// whole delete. The ids need not exist for the limit to bite — the placeholder
// count alone is the problem.
func TestDeleteFlowsOverSQLiteVariableLimit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ids := make([]int64, 32767) // one past SQLITE_MAX_VARIABLE_NUMBER
	for i := range ids {
		ids[i] = int64(100000 + i)
	}
	n, err := s.DeleteFlows(ids)
	if err != nil {
		t.Fatalf("DeleteFlows over the SQLite variable limit: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted %d rows, want 0 (ids do not exist)", n)
	}
}

// A delete spanning multiple chunks must remove every row.
func TestDeleteFlowsAcrossChunkBoundaries(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const n = 450 // spans more than one 400-id chunk
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	deleted, err := s.DeleteFlows(ids)
	if err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}
	if deleted != n {
		t.Fatalf("deleted %d, want %d", deleted, n)
	}
	got, err := s.QueryFlowsFilter(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("after delete: %d flows remain, want 0", len(got))
	}
}
