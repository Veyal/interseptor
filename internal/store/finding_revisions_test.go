package store

import (
	"strings"
	"testing"
)

func TestFindingRevisionsRestoreAndImmutability(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	flow := &Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/example", Status: 200}
	fid, err := s.InsertFlow(flow)
	if err != nil {
		t.Fatal(err)
	}
	f := &Finding{Title: "Original", Impact: "Observed", Tags: []string{"review"}, Targets: FindingTargets{{URL: "https://example.com/example", FlowIDs: []int64{fid}}}}
	id, err := s.CreateFinding(f)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AttachFlow(id, fid, "Recorded result", -1); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListFindingRevisions(id, 100)
	if err != nil || len(before) != 2 {
		t.Fatalf("before %v: %+v", err, before)
	}
	changed := "Revised"
	if err = s.UpdateFindingCanonical(id, nil, nil, &changed, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{Change: FindingChange{Actor: "reviewer", Source: "api", Reason: "Clarify claim"}}); err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListFindingRevisions(id, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 || revs[0].Actor != "reviewer" || revs[0].Reason != "Clarify claim" || !strings.Contains(strings.Join(revs[0].Fields, ","), "title") {
		t.Fatalf("revisions: %+v", revs)
	}
	if _, err = s.db.Exec(`UPDATE finding_revisions SET action='tampered'`); err == nil {
		t.Fatal("revision update permitted")
	}
	if _, err = s.db.Exec(`DELETE FROM finding_revisions`); err == nil {
		t.Fatal("revision deletion permitted")
	}
	if err = s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetFinding(id); err == nil {
		t.Fatal("finding not deleted")
	}
	deleted, err := s.DeletedFindings(100)
	if err != nil || len(deleted) != 1 {
		t.Fatalf("deleted %+v %v", deleted, err)
	}
	if err = s.RestoreFindingRevision(id, before[0].ID, FindingChange{Actor: "reviewer", Source: "api", Reason: "Restore"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Original" || len(got.Flows) != 1 || len(got.Targets) != 1 || len(got.Tags) != 1 {
		t.Fatalf("restore: %+v", got)
	}
	after, _ := s.ListFindingRevisions(id, 100)
	if len(after) != 5 || after[0].Action != "restore" {
		t.Fatalf("after: %+v", after)
	}
}

func TestFindingRevisionRestoreKeepsMissingEvidenceExplicit(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	flow := &Flow{Method: "GET", Host: "example.com", Path: "/"}
	fid, _ := s.InsertFlow(flow)
	f := &Finding{Title: "Evidence"}
	id, _ := s.CreateFinding(f)
	if err := s.AttachFlow(id, fid, "Observed", -1); err != nil {
		t.Fatal(err)
	}
	revs, _ := s.ListFindingRevisions(id, 100)
	if err := s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, fid); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreFindingRevision(id, revs[0].ID, FindingChange{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	if len(got.Flows) != 1 || !got.Flows[0].Missing || got.Flows[0].Note != "Observed" {
		t.Fatalf("missing flow projection lost: %+v", got.Flows)
	}
	if len(got.Blocks) != 1 || !got.Blocks[0].Missing {
		t.Fatalf("missing reference lost: %+v", got.Blocks)
	}
}

func TestFindingRevisionRestoresLegacyAttachmentsAndVerification(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	flow := &Flow{Method: "GET", Host: "example.com", Path: "/legacy"}
	fid, _ := s.InsertFlow(flow)
	id, _ := s.CreateFinding(&Finding{Title: "Legacy"})
	if _, err := s.db.Exec(`INSERT INTO finding_flows(finding_id,flow_id,ord,note) VALUES(?,?,0,'Legacy annotation')`, id, fid); err != nil {
		t.Fatal(err)
	}
	_, err := s.SaveFindingVerification(&FindingVerification{FindingID: id, Gates: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetFindingVerification(id); err == nil {
		t.Fatal("orphan verification survived deletion")
	}
	revs, _ := s.ListFindingRevisions(id, 100)
	if err = s.RestoreFindingRevision(id, revs[0].ID, FindingChange{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	if len(got.Flows) != 1 || got.Flows[0].Note != "Legacy annotation" || got.Verification == nil {
		t.Fatalf("legacy restoration lost evidence %+v", got)
	}
}

func TestFindingRevisionRestoresMixedEvidenceOrder(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	first, _ := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/first"})
	second, _ := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/second"})
	body, _ := MarshalFindingBlocks([]FindingBlock{{Type: "flow", FlowID: first, Note: "First"}, {Type: "flow", FlowID: second, Note: "Second"}})
	id, err := s.CreateFinding(&Finding{Title: "Ordered", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	revisions, _ := s.ListFindingRevisions(id, 100)
	if err = s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM flows WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreFindingRevision(id, revisions[0].ID, FindingChange{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	if len(got.Flows) != 2 || got.Flows[0].FlowID != first || got.Flows[1].FlowID != second || got.Flows[1].Ord != 1 {
		t.Fatalf("mixed evidence order changed: %+v", got.Flows)
	}
}
