package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestFindingImpactRoundTrip verifies that the impact field is persisted on
// create and can be updated independently of all other fields.
func TestFindingImpactRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Create with impact set.
	id, err := s.CreateFinding(&Finding{
		Severity: "High",
		Title:    "Impact round-trip",
		Impact:   "attacker can read all users' PII",
	})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.Impact != "attacker can read all users' PII" {
		t.Fatalf("create: impact want %q got %q", "attacker can read all users' PII", got.Impact)
	}

	// Update impact.
	newImpact := "attacker gains admin access — full account takeover"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, &newImpact, nil); err != nil {
		t.Fatalf("UpdateFinding impact: %v", err)
	}
	got2, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding after update: %v", err)
	}
	if got2.Impact != newImpact {
		t.Fatalf("update: impact want %q got %q", newImpact, got2.Impact)
	}
	// Other fields not clobbered.
	if got2.Title != "Impact round-trip" {
		t.Fatalf("title clobbered after impact update: %q", got2.Title)
	}
}

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
	if err := s.AttachFlow(id, f1, "baseline as user 1", -1); err != nil {
		t.Fatalf("AttachFlow f1: %v", err)
	}
	if err := s.AttachFlow(id, f2, "read user 2's data", -1); err != nil {
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
	if err := s.AttachFlow(id, f1, "updated note", -1); err != nil {
		t.Fatalf("re-AttachFlow: %v", err)
	}
	got, _ = s.GetFinding(id)
	if len(got.Flows) != 2 || got.Flows[0].Note != "updated note" {
		t.Fatalf("re-attach should update note, not duplicate: %+v", got.Flows)
	}

	// Update status; list filter by status.
	verified := "verified"
	if err := s.UpdateFinding(id, nil, &verified, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
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

// TestFindingFlowMissingAfterPurge verifies that when a PoC flow is purged from
// history (deleted from the flows table) but its finding_flows attachment and the
// body flow block survive, reading the finding marks the flow block as Missing.
func TestFindingFlowMissingAfterPurge(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	keep, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/keep", Status: 200})
	gone, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "t.com", Path: "/gone", Status: 200})

	id, err := s.CreateFinding(&Finding{Severity: "High", Title: "PoC purge test", Target: "t.com"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlow(id, keep, "present evidence", -1); err != nil {
		t.Fatalf("AttachFlow keep: %v", err)
	}
	if err := s.AttachFlow(id, gone, "soon-purged evidence", -1); err != nil {
		t.Fatalf("AttachFlow gone: %v", err)
	}

	// Purge one flow from history (simulating prune_history / GC). The finding_flows
	// row and the body flow block are intentionally left intact.
	if _, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, gone); err != nil {
		t.Fatalf("delete flow: %v", err)
	}

	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}

	// Both attachment rows survive; only the purged one is Missing.
	if len(got.Flows) != 2 {
		t.Fatalf("expected 2 attachment rows preserved, got %d", len(got.Flows))
	}
	var sawMissing, sawPresent bool
	for _, fl := range got.Flows {
		switch fl.FlowID {
		case keep:
			if fl.Missing {
				t.Fatalf("kept flow should not be Missing: %+v", fl)
			}
			if fl.Path != "/keep" {
				t.Fatalf("kept flow lost metadata: %+v", fl)
			}
			sawPresent = true
		case gone:
			if !fl.Missing {
				t.Fatalf("purged flow should be Missing: %+v", fl)
			}
			if fl.Note != "soon-purged evidence" {
				t.Fatalf("purged flow lost its annotation: %+v", fl)
			}
			sawMissing = true
		}
	}
	if !sawMissing || !sawPresent {
		t.Fatalf("expected one present + one missing flow, got %+v", got.Flows)
	}

	// The narrative body flow block for the purged flow is marked Missing too, with
	// its note preserved; the present flow's block stays enriched.
	var missBlock, keepBlock *FindingBlock
	for i := range got.Blocks {
		if got.Blocks[i].Type != "flow" {
			continue
		}
		switch got.Blocks[i].FlowID {
		case gone:
			missBlock = &got.Blocks[i]
		case keep:
			keepBlock = &got.Blocks[i]
		}
	}
	if missBlock == nil || !missBlock.Missing {
		t.Fatalf("body flow block for purged flow should be Missing: %+v", got.Blocks)
	}
	if missBlock.Note != "soon-purged evidence" {
		t.Fatalf("missing block annotation not preserved: %+v", missBlock)
	}
	if keepBlock == nil || keepBlock.Missing {
		t.Fatalf("body flow block for kept flow should not be Missing: %+v", got.Blocks)
	}
}

// TestNormalizeFindingSeverityCritical verifies "critical" → "Critical" is
// recognized and that the other severities are unaffected.
func TestNormalizeFindingSeverityCritical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"critical", "Critical"},
		{"CRITICAL", "Critical"},
		{"Critical", "Critical"},
		{"high", "High"},
		{"High", "High"},
		{"medium", "Medium"},
		{"", "Medium"}, // default
		{"low", "Low"},
		{"info", "Info"},
		{"informational", "Info"},
	}
	for _, c := range cases {
		if got := normalizeFindingSeverity(c.in); got != c.want {
			t.Fatalf("normalizeFindingSeverity(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFindingCvssRoundTrip verifies the cvss field persists on create/update.
func TestFindingCvssRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.CreateFinding(&Finding{
		Severity: "Critical",
		Title:    "CVSS round-trip",
		Cvss:     "9.8",
	})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.Cvss != "9.8" {
		t.Fatalf("create: cvss want %q got %q", "9.8", got.Cvss)
	}
	if got.Severity != "Critical" {
		t.Fatalf("create: severity want Critical got %q", got.Severity)
	}

	vector := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, &vector); err != nil {
		t.Fatalf("UpdateFinding cvss: %v", err)
	}
	got2, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding after update: %v", err)
	}
	if got2.Cvss != vector {
		t.Fatalf("update: cvss want %q got %q", vector, got2.Cvss)
	}
}

// TestUpdateFindingBodyPreservesFlowOrder verifies that updating a finding via
// the "detail" field only replaces the first text block in the existing body,
// leaving all flow blocks in their original positions.
func TestUpdateFindingBodyPreservesFlowOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	f1, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/a", Status: 200})
	f2, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Host: "t.com", Path: "/b", Status: 403})

	id, err := s.CreateFinding(&Finding{Title: "flow order test", Detail: "original detail"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlow(id, f1, "first poc", -1); err != nil {
		t.Fatalf("AttachFlow f1: %v", err)
	}
	if err := s.AttachFlow(id, f2, "second poc", -1); err != nil {
		t.Fatalf("AttachFlow f2: %v", err)
	}

	// Body is now: [text:"original detail", flow:f1, flow:f2]
	// Update detail only (no body arg) — must update in-place, not append or reorder.
	newDetail := "updated detail"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, &newDetail, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateFinding detail: %v", err)
	}

	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if len(got.Blocks) != 3 {
		t.Fatalf("expected 3 blocks (text+flow+flow), got %d: %+v", len(got.Blocks), got.Blocks)
	}
	if got.Blocks[0].Type != "text" || got.Blocks[0].MD != "updated detail" {
		t.Fatalf("block[0] should be updated text, got %+v", got.Blocks[0])
	}
	if got.Blocks[1].Type != "flow" || got.Blocks[1].FlowID != f1 {
		t.Fatalf("block[1] should be flow f1, got %+v", got.Blocks[1])
	}
	if got.Blocks[2].Type != "flow" || got.Blocks[2].FlowID != f2 {
		t.Fatalf("block[2] should be flow f2, got %+v", got.Blocks[2])
	}
}

// TestAttachFlowAtPosition verifies that the position argument to AttachFlow
// inserts the flow block at the correct 0-based index.
func TestAttachFlowAtPosition(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	f1, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/1", Status: 200})
	f2, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "t.com", Path: "/2", Status: 200})
	f3, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "t.com", Path: "/3", Status: 200})

	// Build a body: [text:"intro", flow:f1, text:"middle"]
	body, _ := json.Marshal([]blockRecord{
		{Type: "text", MD: "intro"},
		{Type: "flow", FlowID: f1},
		{Type: "text", MD: "middle"},
	})
	id, err := s.CreateFinding(&Finding{Title: "position test", Body: string(body)})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	// Insert f2 at position 1 → [text, flow:f2, flow:f1, text]
	if err := s.AttachFlow(id, f2, "inserted at 1", 1); err != nil {
		t.Fatalf("AttachFlow pos=1: %v", err)
	}
	got, _ := s.GetFinding(id)
	if len(got.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(got.Blocks))
	}
	if got.Blocks[1].Type != "flow" || got.Blocks[1].FlowID != f2 {
		t.Fatalf("block[1] should be f2, got %+v", got.Blocks[1])
	}
	if got.Blocks[2].Type != "flow" || got.Blocks[2].FlowID != f1 {
		t.Fatalf("block[2] should be f1, got %+v", got.Blocks[2])
	}

	// Append f3 (pos=-1) → f3 goes at end
	if err := s.AttachFlow(id, f3, "appended", -1); err != nil {
		t.Fatalf("AttachFlow append: %v", err)
	}
	got, _ = s.GetFinding(id)
	last := got.Blocks[len(got.Blocks)-1]
	if last.Type != "flow" || last.FlowID != f3 {
		t.Fatalf("last block should be f3, got %+v", last)
	}

	// Re-attach f2 with new note — position must not change (idempotent).
	if err := s.AttachFlow(id, f2, "updated note", 99); err != nil {
		t.Fatalf("re-AttachFlow: %v", err)
	}
	got, _ = s.GetFinding(id)
	// f2 should still be at index 1.
	if got.Blocks[1].Type != "flow" || got.Blocks[1].FlowID != f2 || got.Blocks[1].Note != "updated note" {
		t.Fatalf("re-attach should update note in-place at block[1], got %+v", got.Blocks)
	}
}

// TestUpdateFindingWithBodyArg verifies that passing "body" directly to
// UpdateFinding replaces the full narrative body and syncs detail from it.
func TestUpdateFindingWithBodyArg(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.CreateFinding(&Finding{Title: "body arg test", Detail: "old"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	newBody := `[{"type":"text","md":"first block"},{"type":"text","md":"second block"}]`
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &newBody, nil, nil); err != nil {
		t.Fatalf("UpdateFinding body: %v", err)
	}

	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if len(got.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(got.Blocks))
	}
	if got.Blocks[0].MD != "first block" {
		t.Fatalf("block[0].MD want %q got %q", "first block", got.Blocks[0].MD)
	}
	// detail must be synced from first text block
	if !strings.Contains(got.Detail, "first block") {
		t.Fatalf("detail not synced from body: %q", got.Detail)
	}
}
