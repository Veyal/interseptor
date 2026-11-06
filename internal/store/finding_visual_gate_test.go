package store

import (
	"slices"
	"testing"
)

// xssFinding is a complete finding whose impact is only visible in a client.
func xssFinding() Finding {
	f := completeAssessment()
	f.Cwe = "CWE-79"
	f.Title = "Stored XSS in the profile bio"
	f.Blocks = []FindingBlock{
		{Type: "flow", FlowID: 1, Role: "action", Proof: "Saved the payload in the bio field"},
		{Type: "flow", FlowID: 3, Role: "control", Proof: "The same payload is escaped for a plain user"},
	}
	return f
}

// A finding that claims script execution must show the script executing: an
// attached flow only proves the payload was stored and echoed.
func TestClientSideFindingRequiresACaptureOfTheEffect(t *testing.T) {
	f := xssFinding()
	f.Blocks = append(f.Blocks, FindingBlock{Type: "flow", FlowID: 2, Role: "result", Proof: "The response echoes the payload unescaped"})
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: "render-1", Role: "result", Source: "flow_preview", Proof: "Rendered HTTP preview of the echo"})
	r := f.ReadinessSummary()
	if r.Stage == "report_ready" || !slices.Contains(r.Gaps, "visual") {
		t.Fatalf("an echoed payload is not execution; want a visual gap: %+v", r)
	}
}

func TestClientSideFindingIsReadyWithABrowserCapture(t *testing.T) {
	f := xssFinding()
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: "shot-1", Role: "result", Source: "browser_screenshot", Proof: "Alert dialog fired when the profile page loaded"})
	// The claim machinery also asks the reviewer to say what the capture shows.
	f.ProofReview.Claims = map[string]FindingCapabilityClaim{"browser_execution": {
		Note:     "Chromium fired the alert on page load",
		Evidence: []FindingEvidenceReference{{Hash: "shot-1"}},
	}}
	if r := f.ReadinessSummary(); r.Stage != "report_ready" || len(r.Gaps) != 0 {
		t.Fatalf("a real browser capture should satisfy the gate: %+v", r)
	}
}

// An uploaded image and a generated preview are not browser proof, whatever
// their caption says.
func TestClientSideFindingRejectsNonCaptureImages(t *testing.T) {
	for _, source := range []string{"operator_upload", "tool_output", "evidence_render", "generated_image"} {
		f := xssFinding()
		f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: "img-" + source, Role: "result", Source: source, Proof: "Screenshot of the effect"})
		if r := f.ReadinessSummary(); !slices.Contains(r.Gaps, "visual") {
			t.Errorf("source %q must not satisfy the visual gate: %+v", source, r.Gaps)
		}
	}
}

// The gate is about client-side impact, not about every finding: an IDOR with
// flow evidence stays report-ready without a screenshot.
func TestNonClientSideFindingNeedsNoVisualCapture(t *testing.T) {
	f := completeAssessment()
	f.Cwe = "CWE-639"
	f.Title = "IDOR on the invoice endpoint"
	if r := f.ReadinessSummary(); r.Stage != "report_ready" || slices.Contains(r.Gaps, "visual") {
		t.Fatalf("an HTTP finding must not require a browser capture: %+v", r)
	}
}

// Declaring proofReview.visual keeps working for classes with no CWE.
func TestVisualDeclarationStillRequiresACapture(t *testing.T) {
	f := completeAssessment()
	f.ProofReview.Visual = true
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: "preview-1", Role: "result", Source: "flow_preview", Proof: "Rendered preview"})
	if r := f.ReadinessSummary(); !slices.Contains(r.Gaps, "visual") {
		t.Fatalf("a visual declaration must still need a real capture: %+v", r)
	}
}
