package store

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestScreenshotClassificationPreservesUploadOrigin(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	f := &Finding{Title: "Capture"}
	id, _ := s.CreateFinding(f)
	hash, _, err := s.PutAndAttachImage(id, "image/png", tinyPNG, "Recorded state", -1, "result", "Visible result", "operator_upload", 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	p := got.Blocks[0].Provenance
	if p == nil || p.Ingestion != "upload" || p.OriginalSource != "operator_upload" || p.IngestedTS == 0 {
		t.Fatalf("upload provenance: %+v", p)
	}
	blocks := got.Blocks
	blocks[0].Source = "device_screenshot"
	blocks[0].Provenance = &FindingImageProvenance{Ingestion: "forged", ClassifiedBy: "forged"}
	body, err := MarshalFindingBlocks(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{Change: FindingChange{Actor: "reviewer", Source: "http"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	p = got.Blocks[0].Provenance
	if p.Ingestion != "upload" || p.OriginalSource != "operator_upload" || p.ClassifiedBy != "reviewer" || p.ClassifiedTS == 0 || got.Readiness.ScreenshotCount != 1 {
		t.Fatalf("classification %+v %+v", p, got.Readiness)
	}
	if err = s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	roots, err := s.FindingImageHashes()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := roots[hash]; !ok {
		t.Fatal("deleted revision lost image GC root")
	}
}
func TestGeneratedImageCannotBecomeCaptureAcrossFindings(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	a, _ := s.CreateFinding(&Finding{Title: "Generated"})
	b, _ := s.CreateFinding(&Finding{Title: "Other"})
	_, _, err := s.PutAndAttachImage(a, "image/png", tinyPNG, "Preview", -1, "result", "Preview only", "flow_preview", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.PutAndAttachImage(b, "image/png", tinyPNG, "Relabel", -1, "result", "Claimed capture", "browser_screenshot", 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(b)
	if got.Blocks[0].Source != "flow_preview" || got.Readiness.ScreenshotCount != 0 {
		b, _ := json.Marshal(got.Blocks)
		t.Fatalf("generated provenance lost: %s", b)
	}
}

func TestClassifyFindingImageRelabelsWithoutReupload(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Title: "Capture"})
	hash, _, err := s.PutAndAttachImage(id, "image/png", tinyPNG, "Recorded state", -1, "result", "Visible result", "operator_upload", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFinding(id); got.Readiness.ScreenshotCount != 0 {
		t.Fatalf("unclassified upload counted: %+v", got.Readiness)
	}
	if err = s.ClassifyFindingImage(id, hash, "browser_screenshot", FindingChange{Actor: "reviewer", Source: "http"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	p := got.Blocks[0].Provenance
	if got.Blocks[0].Source != "browser_screenshot" || got.Readiness.ScreenshotCount != 1 || p.Ingestion != "upload" || p.OriginalSource != "operator_upload" || p.ClassifiedBy != "reviewer" {
		t.Fatalf("classify: %+v %+v %+v", got.Blocks[0], p, got.Readiness)
	}
}

func TestClassifyFindingImageRejectsGeneratedAndUnknown(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Title: "Preview"})
	hash, _, err := s.PutAndAttachImage(id, "image/png", tinyPNG, "Preview", -1, "result", "Preview only", "flow_preview", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ClassifyFindingImage(id, hash, "browser_screenshot", FindingChange{Actor: "reviewer"}); err == nil {
		t.Fatal("generated preview was relabelled as a capture")
	}
	if err = s.ClassifyFindingImage(id, strings.Repeat("b", 64), "browser_screenshot", FindingChange{}); err == nil {
		t.Fatal("unknown image accepted")
	}
	if err = s.ClassifyFindingImage(id, hash, "flow_preview", FindingChange{}); err == nil {
		t.Fatal("generated target source accepted")
	}
	if got, _ := s.GetFinding(id); got.Readiness.ScreenshotCount != 0 {
		t.Fatalf("flow preview counted as screenshot: %+v", got.Readiness)
	}
}

func TestMissingImageMappingsAndRawMessagesAgreeWithReadiness(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Title: "Evidence integrity"})
	hash, _, err := s.PutAndAttachImage(id, "image/png", tinyPNG, "Capture", -1, "result", "Observed image", "browser_screenshot", 0)
	if err != nil {
		t.Fatal(err)
	}
	review := FindingProofReview{Evidence: map[string]FindingEvidenceReference{"result": {Hash: hash}}, Claims: map[string]FindingCapabilityClaim{"browser_execution": {Note: "Reviewer observation", Evidence: []FindingEvidenceReference{{Hash: hash}}}}}
	if err = s.UpdateFindingMetadata(id, FindingMetadataPatch{ProofReview: &review}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(s.bodyPath(hash)); err != nil {
		t.Fatal(err)
	}
	flow := &Flow{Method: "GET", Host: "example.com", Path: "/evidence", ResLen: 10, ResBodyHash: strings.Repeat("a", 64)}
	fid, err := s.InsertFlow(flow)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AttachFlow(id, fid, "Recorded response", -1); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if !f.ProofReview.Evidence["result"].Missing || !f.ProofReview.Claims["browser_execution"].Evidence[0].Missing {
		t.Fatal("missing image mapping appeared valid")
	}
	if len(f.Blocks) != 2 || !f.Blocks[1].RawMissing || !slices.Contains(f.Missing, "evidence_missing") {
		t.Fatalf("missing raw body did not block quality gate: %+v", f)
	}
}
