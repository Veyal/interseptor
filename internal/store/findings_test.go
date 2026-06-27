package store

import (
	"testing"
	"time"
)

func TestFindingsCRUDAndPoCFlows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Two flows to attach as PoC evidence.
	f1, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/user/1", Status: 200})
	f2, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "t.com", Path: "/user/2", Status: 200})

	id, err := s.CreateFinding(&Finding{Severity: "high", Status: "open", Source: "ai", Title: "IDOR on /user/{id}", Target: "t.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	// Attach both flows as PoCs; one with a note.
	if err := s.AttachFlow(id, f1, "baseline as user 1"); err != nil {
		t.Fatalf("AttachFlow f1: %v", err)
	}
	if err := s.AttachFlow(id, f2, "read user 2's data"); err != nil {
		t.Fatalf("AttachFlow f2: %v", err)
	}

	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.Severity != "High" || got.Status != "open" || got.Source != "ai" {
		t.Fatalf("normalization wrong: %+v", got)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("expected 2 PoC flows, got %d", len(got.Flows))
	}
	// PoC flows are enriched with the flow summary and ordered.
	if got.Flows[0].FlowID != f1 || got.Flows[0].Path != "/user/1" || got.Flows[0].Note != "baseline as user 1" {
		t.Fatalf("PoC[0] wrong: %+v", got.Flows[0])
	}
	if got.Flows[1].FlowID != f2 || got.Flows[1].Path != "/user/2" {
		t.Fatalf("PoC[1] wrong: %+v", got.Flows[1])
	}

	// Re-attach updates the note, doesn't duplicate.
	if err := s.AttachFlow(id, f1, "updated note"); err != nil {
		t.Fatalf("re-AttachFlow: %v", err)
	}
	got, _ = s.GetFinding(id)
	if len(got.Flows) != 2 || got.Flows[0].Note != "updated note" {
		t.Fatalf("re-attach should update note, not duplicate: %+v", got.Flows)
	}

	// Update status; list filter by status.
	verified := "verified"
	if err := s.UpdateFinding(id, nil, &verified, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateFinding: %v", err)
	}
	open, _ := s.ListFindings("", "open")
	if len(open) != 0 {
		t.Fatalf("status filter: expected 0 open, got %d", len(open))
	}
	ver, _ := s.ListFindings("", "verified")
	if len(ver) != 1 || ver[0].UpdatedTS < ver[0].TS {
		t.Fatalf("status filter: expected 1 verified with bumped updated_ts, got %+v", ver)
	}

	// Detach one PoC.
	if err := s.DetachFlow(id, f1); err != nil {
		t.Fatalf("DetachFlow: %v", err)
	}
	got, _ = s.GetFinding(id)
	if len(got.Flows) != 1 || got.Flows[0].FlowID != f2 {
		t.Fatalf("after detach expected only f2, got %+v", got.Flows)
	}

	// Delete finding removes its attachments.
	if err := s.DeleteFinding(id); err != nil {
		t.Fatalf("DeleteFinding: %v", err)
	}
	all, _ := s.ListFindings("", "")
	if len(all) != 0 {
		t.Fatalf("expected 0 findings after delete, got %d", len(all))
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM finding_flows WHERE finding_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("expected PoC attachments gone after delete, got %d", n)
	}
}
