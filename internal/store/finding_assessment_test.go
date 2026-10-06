package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

const testCVSS4 = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"

func completeAssessment() Finding {
	return Finding{Title: "Example", Summary: "Observed behavior", Target: "https://example.com/a", Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary", Fix: "Correct the boundary", Retest: "Confirm secure behavior", Confidence: "certain", Cvss: testCVSS4, ProofReview: FindingProofReview{Execution: "demonstrated"}, Blocks: []FindingBlock{
		{Type: "flow", FlowID: 1, Role: "action", Proof: "Recorded action"},
		{Type: "flow", FlowID: 2, Role: "result", Proof: "Observed result"},
		{Type: "flow", FlowID: 3, Role: "control", Proof: "Expected control"},
	}}
}

func TestFindingCapabilityReadiness(t *testing.T) {
	f := completeAssessment()
	f.EnrichCompleteness()
	if !f.Ready || f.CvssScore == nil || *f.CvssScore != 9.3 {
		t.Fatalf("complete: %+v", f)
	}
	for _, role := range []string{"action", "result", "control"} {
		f = completeAssessment()
		for i := range f.Blocks {
			if f.Blocks[i].Role == role {
				f.Blocks[i].Role = "context"
			}
		}
		f.EnrichCompleteness()
		if f.Ready || !slices.Contains(f.Missing, role) {
			t.Fatalf("missing %s: %+v", role, f.Readiness)
		}
	}
	f = completeAssessment()
	f.ProofReview.Visual = true
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: "example", Source: "flow_preview", Role: "result", Proof: "Rendered HTTP preview"})
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "visual") {
		t.Fatal("generated preview qualified as real visual proof")
	}
	f.Blocks[3].Source = "operator_upload"
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "visual") {
		t.Fatal("unclassified upload qualified as browser capture")
	}
	f.Blocks[3].Source = "browser_screenshot"
	f.EnrichCompleteness()
	if !f.Ready {
		t.Fatalf("declared browser screenshot: %+v", f.Readiness)
	}
	f.Blocks[3].Role = "action"
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "visual") {
		t.Fatal("action screenshot substituted for visual result")
	}
	f.Blocks[3].Role = "result"
	f.Severity = "Low"
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "severity") {
		t.Fatal("severity mismatch accepted")
	}
	f.Cvss = "9.8"
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "cvss") {
		t.Fatal("legacy score counted as v4 vector")
	}
}

func TestReadinessReportsCapabilitiesSeparately(t *testing.T) {
	f := completeAssessment()
	f.Blocks[2].Role = "context" // drop the control case
	f.ProofReview.Visual = true
	f.Blocks = append(f.Blocks,
		FindingBlock{Type: "image", Hash: "c1", Source: "flow_preview", Role: "result", Proof: "Rendered"},
		FindingBlock{Type: "image", Hash: "c2", Source: "operator_upload", Role: "result", Proof: "Uploaded"})
	f.EnrichCompleteness()
	c := f.Readiness.Capabilities
	if !c.Action || !c.Result || c.Control || c.Visual {
		t.Fatalf("capabilities %+v", c)
	}
	if f.Readiness.GeneratedImageCount != 1 || f.Readiness.UploadedImageCount != 1 || f.Readiness.ScreenshotCount != 0 {
		t.Fatalf("image labelling %+v", f.Readiness)
	}
	f.Blocks[4].Source = "browser_screenshot"
	f.ProofReview.Execution = "not_executed"
	f.ProofReview.Reason = "End-to-end step not run: no safe test account"
	f.Status = "needs_verification"
	f.EnrichCompleteness()
	if !f.Readiness.Capabilities.Visual || f.Readiness.Capabilities.Execution != "not_executed" || f.Ready || !slices.Contains(f.Missing, "verification") {
		t.Fatalf("not_executed path %+v", f.Readiness)
	}
	if slices.Contains(f.Missing, "execution_reason") {
		t.Fatal("reason given but still flagged")
	}
}

func TestFindingTargetsAndExecutionPersist(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := Finding{Title: "Targets", Environment: "development", Status: "verified", ProofReview: FindingProofReview{Execution: "not_executed", Reason: "Required evidence is unavailable"}, Targets: FindingTargets{
		{URL: "https://example.com/a", Methods: []string{"get", "POST"}, Role: "reader", Relation: "affected", Variant: "record"},
		{URL: "https://example.com/setup", Relation: "setup", EvidenceException: "Setup only; impact is recorded on the affected endpoint"},
	}}
	id, err := s.CreateFinding(&f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "needs_verification" || got.Target != "https://example.com/a" || len(got.Targets) != 2 || got.Targets[0].Methods[0] != "GET" || got.ProofReview.Reason != f.ProofReview.Reason {
		t.Fatalf("roundtrip: %+v", got)
	}
	verified := "verified"
	if err := s.UpdateFinding(id, nil, &verified, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	if got.Status != "needs_verification" {
		t.Fatal("status-only update bypassed unexecuted assessment")
	}
	got.Targets[0], got.Targets[1] = got.Targets[1], got.Targets[0]
	if err := s.UpdateFindingMetadata(id, FindingMetadataPatch{Targets: &got.Targets}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	if got.Target != "https://example.com/setup" {
		t.Fatal("reordering did not update primary target")
	}
	if !slices.Contains(got.Readiness.Gaps, "target_evidence") || len(got.Readiness.TargetEvidenceGaps) != 1 || got.Readiness.TargetEvidenceGaps[0] != 1 {
		t.Fatalf("target evidence gaps: %+v", got.Readiness)
	}
	bad := FindingProofReview{Execution: "not_executed"}
	if err := s.UpdateFindingMetadata(id, FindingMetadataPatch{ProofReview: &bad}); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("missing reason accepted: %v", err)
	}
	legacy, err := s.CreateFinding(&Finding{Title: "Legacy", Target: "Original app label"})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetFinding(legacy)
	if len(old.Targets) != 1 || old.Targets[0].URL != "Original app label" {
		t.Fatal("legacy target lost")
	}
}

func TestFindingEvidenceMappingReusesOneCapture(t *testing.T) {
	f := completeAssessment()
	f.Blocks = f.Blocks[:1]
	f.Blocks[0].Role = "context"
	f.ProofReview.Evidence = map[string]FindingEvidenceReference{"action": {FlowID: 1}, "result": {FlowID: 1}, "control": {FlowID: 1}}
	f.EnrichCompleteness()
	if !f.Ready {
		t.Fatalf("explicitly mapped capture: %+v", f.Readiness)
	}
	f.ProofReview.Evidence["result"] = FindingEvidenceReference{FlowID: 2}
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "result") {
		t.Fatal("unattached mapping accepted")
	}
}

func TestFindingAssessmentArchiveSnapshot(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := Finding{Title: "Snapshot", Environment: "testing", Targets: FindingTargets{{URL: "https://example.com/a", Methods: []string{"GET"}}, {URL: "https://example.com/b", Role: "reviewer", Relation: "setup", EvidenceException: "Preparation only"}}, ProofReview: FindingProofReview{Execution: "not_executed", Reason: "Review pending"}}
	id, err := s.CreateFinding(&f)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.BackupTo(filepath.Join(dir, currentDBName)); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 2 || got.Targets[1].Role != "reviewer" || got.Environment != "testing" || got.ProofReview.Reason != "Review pending" {
		t.Fatalf("archive metadata lost: %+v", got)
	}
}

func TestFindingTargetRejectsUnknownFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateFinding(&Finding{Title: "Unknown reference", Targets: FindingTargets{{URL: "https://example.com/a", FlowIDs: []int64{999}}}})
	if !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("unresolved target flow: %v", err)
	}
}

func TestFindingAssessmentMergeRemapsAndPreservesMissing(t *testing.T) {
	peerDir := t.TempDir()
	peer, err := Open(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	id, err := peer.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/peer"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := MarshalFindingBlocks([]FindingBlock{{Type: "flow", FlowID: id, Role: "result", Proof: "Recorded example response"}})
	f := Finding{Title: "Peer evidence", Environment: "development", Body: body, Targets: FindingTargets{{URL: "https://example.com/peer", Methods: []string{"GET"}, FlowIDs: []int64{id}}, {URL: "https://example.com/missing", FlowIDs: []int64{99}, MissingFlowIDs: []int64{99}}}, ProofReview: FindingProofReview{Execution: "not_executed", Reason: "Review pending", Evidence: map[string]FindingEvidenceReference{"result": {FlowID: id}, "control": {FlowID: 99, Missing: true}}}}
	if _, err := peer.CreateFinding(&f); err != nil {
		t.Fatal(err)
	}
	local, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for i := 0; i < 2; i++ {
		if _, err := local.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/local"}); err != nil {
			t.Fatal(err)
		}
	}
	// A purged peer reference collides with an unrelated local capture.
	if _, err := local.db.Exec(`UPDATE flows SET id=99 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(peerDir, currentDBName)
	preview, err := local.MergePreview(peerPath, peerBodiesDir(peer), "peer")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := local.MergeFrom(peerPath, peerBodiesDir(peer), "peer")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FindingsAdded != 1 || preview.FindingsAdded != 1 {
		t.Fatalf("counts: %+v %+v", stats, preview)
	}
	fs, err := local.ListFindings("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got := fs[0]
	if got.Targets[0].FlowIDs[0] == id || got.ProofReview.Evidence["result"].FlowID != got.Targets[0].FlowIDs[0] || got.Environment != "development" || !got.ProofReview.Evidence["control"].Missing || !slices.Contains(got.Targets[1].MissingFlowIDs, 99) {
		t.Fatalf("merge metadata: %+v", got)
	}
	got.Targets[1].MissingFlowIDs = nil
	got.Targets[1].URL = "https://example.com/renamed"
	if err := local.UpdateFindingMetadata(got.ID, FindingMetadataPatch{Targets: &got.Targets}); err != nil {
		t.Fatal(err)
	}
	got2, _ := local.GetFinding(got.ID)
	if !slices.Contains(got2.Targets[1].MissingFlowIDs, 99) {
		t.Fatal("target rename erased missing peer reference")
	}
	stats, err = local.MergeFrom(peerPath, peerBodiesDir(peer), "peer")
	if err != nil {
		t.Fatal(err)
	}
	// Renamed scope is a distinct finding; the original imports exactly once.
	if stats.FindingsAdded != 1 {
		t.Fatalf("changed target dropped by dedupe: %+v", stats)
	}
	stats, err = local.MergeFrom(peerPath, peerBodiesDir(peer), "peer")
	if err != nil || stats.FindingsAdded != 0 {
		t.Fatalf("repeated merge: %+v %v", stats, err)
	}
}

func TestGeneratedFindingImageCannotBeRelabeled(t *testing.T) {
	old := marshalBody([]FindingBlock{{Type: "image", Hash: "example", Source: "flow_preview", SourceFlowID: 1}})
	next := marshalBody([]FindingBlock{{Type: "image", Hash: "example", Source: "browser_screenshot"}})
	for _, body := range []string{preserveMissingFlowMarkers(old, next), insertImageIntoBodyWithMetadata(old, "example", "image/png", "Updated caption", -1, "result", "Observed response", "browser_screenshot", 9)} {
		blocks := buildBlocks(body, "", "", nil)
		if blocks[0].Source != "flow_preview" || blocks[0].SourceFlowID != 1 {
			t.Fatal("generated preview was reclassified")
		}
	}
}

func TestFindingCVSS4CalculatedBands(t *testing.T) {
	for _, tc := range []struct {
		vector   string
		score    float64
		severity string
	}{
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N", 0, "Info"},
		{"CVSS:4.0/AV:P/AC:H/AT:P/PR:H/UI:A/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N", 1, "Low"},
		{"CVSS:4.0/AV:L/AC:L/AT:N/PR:L/UI:P/VC:N/VI:H/VA:H/SC:N/SI:L/SA:L", 5.2, "Medium"},
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:N/SC:H/SI:H/SA:H", 7.9, "High"},
		{testCVSS4, 9.3, "Critical"},
	} {
		f := completeAssessment()
		f.Cvss = tc.vector
		f.Severity = tc.severity
		f.EnrichCompleteness()
		if f.CvssScore == nil || *f.CvssScore != tc.score || !f.Ready {
			t.Fatalf("%s: score=%v readiness=%+v", tc.severity, f.CvssScore, f.Readiness)
		}
	}
}
