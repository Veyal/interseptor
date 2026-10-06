package store

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/rendermark"
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

func TestEvidenceRenderIsGeneratedWithImmutableSourceRef(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateFinding(&Finding{Title: "Rate limit"})
	hash, _, err := s.PutAndAttachImageRef(id, "image/png", tinyPNG, "Timeline", -1, "result", "Generated from recorded data", "evidence_render", 0, "intruder:run-01")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	b := got.Blocks[0]
	if b.Source != "evidence_render" || b.SourceRef != "intruder:run-01" {
		t.Fatalf("block: %+v", b)
	}
	if b.Provenance == nil || b.Provenance.Ingestion != "generated" || b.Provenance.SourceRef != "intruder:run-01" {
		t.Fatalf("provenance: %+v", b.Provenance)
	}
	if got.Readiness.Capabilities.Visual || got.Readiness.GeneratedImageCount != 1 || got.Readiness.ScreenshotCount != 0 || got.Readiness.UploadedImageCount != 0 {
		t.Fatalf("render counted as real proof: %+v", got)
	}
	// Re-attach with a different source and ref must not overwrite.
	if _, _, err = s.PutAndAttachImageRef(id, "image/png", tinyPNG, "Timeline", -1, "result", "", "browser_screenshot", 5, "intruder:other"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	b = got.Blocks[0]
	if b.Hash != hash || b.Source != "evidence_render" || b.SourceRef != "intruder:run-01" || b.SourceFlowID != 0 {
		t.Fatalf("overwritten: %+v", b)
	}
	// A body rewrite cannot forge or drop the ref either.
	blocks := got.Blocks
	blocks[0].SourceRef = "intruder:forged"
	body, err := MarshalFindingBlocks(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	if got.Blocks[0].SourceRef != "intruder:run-01" {
		t.Fatalf("sourceRef forged via update: %q", got.Blocks[0].SourceRef)
	}
	// Reclassifying a generated render as a capture is refused.
	if err = s.ClassifyFindingImage(id, hash, "browser_screenshot", FindingChange{}); err == nil {
		t.Fatal("evidence_render reclassified")
	}
}

func TestSourceRefValidationAndLegacyBodies(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateFinding(&Finding{Title: "Ref"})
	for _, ref := range []string{"Intruder:1", "intruder run", "intruder/1", strings.Repeat("a", 81)} {
		if _, _, err := s.PutAndAttachImageRef(id, "image/png", tinyPNG, "x", -1, "result", "", "evidence_render", 0, ref); err == nil {
			t.Fatalf("ref %q accepted", ref)
		}
	}
	if _, err := NormalizeFindingBody(`[{"type":"text","md":"x"},{"type":"image","hash":"` + strings.Repeat("a", 64) + `","source":"evidence_render","sourceRef":"BAD"}]`); err == nil {
		t.Fatal("bad sourceRef in body accepted")
	}
	// Legacy JSON without sourceRef still loads.
	legacy := `[{"type":"text","md":"old finding"},{"type":"image","hash":"` + strings.Repeat("b", 64) + `","source":"flow_preview","sourceFlowId":7}]`
	out, err := NormalizeFindingBody(legacy)
	if err != nil || strings.Contains(out, "sourceRef") {
		t.Fatalf("legacy: %v %s", err, out)
	}
	if !generatedFindingImage("evidence_render") || capturedFindingImage("evidence_render") {
		t.Fatal("evidence_render must be generated, not captured")
	}
}

func TestGeneratedRenderMarkerCannotBeRelabelledAsCapture(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateFinding(&Finding{Title: "Marker"})
	marked := rendermark.Embed(tinyPNG, "generated evidence render; ref=intruder:run-01")
	for _, src := range []string{"browser_screenshot", "device_screenshot", "operator_upload", ""} {
		if _, _, err := s.PutAndAttachImageRef(id, "image/png", marked, "x", -1, "result", "", src, 0, ""); err == nil {
			t.Fatalf("source %q accepted a generated render as a capture", src)
		}
	}
	if _, _, err := s.PutAndAttachImageRef(id, "image/png", marked, "x", -1, "result", "", "evidence_render", 0, "intruder:run-01"); err != nil {
		t.Fatalf("honest evidence_render attach must work: %v", err)
	}
	if _, _, err := s.PutAndAttachImageRef(id, "image/png", tinyPNG, "real", -1, "result", "", "browser_screenshot", 0, ""); err != nil {
		t.Fatalf("an unmarked screenshot must still attach: %v", err)
	}
}
