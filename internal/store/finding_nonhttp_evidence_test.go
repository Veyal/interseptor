package store

import (
	"slices"
	"testing"
)

// A finding about a non-HTTP protocol has no flow to attach and no browser
// screenshot to take: a captured transcript is the honest artefact. Before this,
// an SSH/SMTP/SMB finding could never leave draft no matter how complete it was.
func TestNonHTTPTranscriptFindingCanBeReportReady(t *testing.T) {
	f := Finding{
		Title: "OpenSSH 7.2 on 22/tcp", Summary: "Banner discloses an unpatched OpenSSH", Target: "tcp://example.com:22",
		Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary",
		Fix: "Upgrade OpenSSH", Retest: "Banner reports the fixed version", Confidence: "certain", Cvss: testCVSS4,
		ProofReview: FindingProofReview{Execution: "demonstrated"},
		Targets:     FindingTargets{{URL: "tcp://example.com:22", Methods: []string{"SSH"}}},
		Blocks: []FindingBlock{
			{Type: "text", Role: "action", Source: "tool_output", MD: "$ nc example.com 22", Proof: "Connected to 22/tcp and read the banner"},
			{Type: "text", Role: "result", Source: "tool_output", MD: "SSH-2.0-OpenSSH_7.2", Proof: "The server identified itself as OpenSSH 7.2"},
			{Type: "text", Role: "control", Source: "tool_output", MD: "SSH-2.0-OpenSSH_9.6", Proof: "A patched host reports the fixed banner"},
		},
	}
	r := f.ReadinessSummary()
	if r.Stage != "report_ready" || len(r.Gaps) != 0 {
		t.Fatalf("transcript finding should be report-ready: %+v", r)
	}
}

func TestNonHTTPProtocolCaptureSourceIsAccepted(t *testing.T) {
	f := Finding{
		Title: "POP3 cleartext authentication", Summary: "Credentials cross the wire in cleartext", Target: "tcp://example.com:110",
		Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary",
		Fix: "Require TLS", Retest: "Credentials are refused before STARTTLS", Confidence: "certain", Cvss: testCVSS4,
		ProofReview: FindingProofReview{Execution: "demonstrated"},
		Targets:     FindingTargets{{URL: "tcp://example.com:110"}},
		Blocks: []FindingBlock{
			{Type: "text", Role: "action", Source: "protocol_capture", MD: "USER alice", Proof: "Started a cleartext POP3 session"},
			{Type: "text", Role: "result", Source: "protocol_capture", MD: "+OK PASS accepted", Proof: "The server accepted the password in cleartext"},
			{Type: "text", Role: "control", Source: "protocol_capture", MD: "-ERR TLS required", Proof: "A hardened host refuses cleartext auth"},
		},
	}
	if got := normalizeFindingBlockSource("protocol_capture"); got != protocolCaptureSource {
		t.Fatalf("protocol_capture source normalised to %q", got)
	}
	if r := f.ReadinessSummary(); r.Stage != "report_ready" || len(r.Gaps) != 0 {
		t.Fatalf("protocol capture finding should be report-ready: %+v", r)
	}
}

// A transcript image (an operator screenshot of a terminal, a tool render) may
// evidence a role or a target, but it is not a visual claim about a browser.
func TestUploadedTranscriptImageEvidencesButIsNotVisual(t *testing.T) {
	f := Finding{
		Title: "Redis unauthenticated", Summary: "The instance answers without auth", Target: "tcp://example.com:6379",
		Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary",
		Fix: "Require a password", Retest: "PING is refused without auth", Confidence: "certain", Cvss: testCVSS4,
		ProofReview: FindingProofReview{Execution: "demonstrated"},
		Targets:     FindingTargets{{URL: "tcp://example.com:6379", ImageHashes: []string{"shot-1"}}},
		Blocks: []FindingBlock{
			{Type: "text", Role: "action", Source: "tool_output", Proof: "Sent PING without credentials"},
			{Type: "text", Role: "control", Source: "tool_output", Proof: "A protected instance refuses PING"},
			{Type: "image", Hash: "shot-1", Role: "result", Source: "tool_output", Proof: "Terminal transcript of PONG"},
		},
	}
	if r := f.ReadinessSummary(); r.Stage != "report_ready" || len(r.Gaps) != 0 {
		t.Fatalf("uploaded transcript should evidence the result and the target: %+v", r)
	}
	if f.capabilityStatus(nil).Visual {
		t.Fatal("an uploaded transcript must not count as visual proof")
	}
}

// Prose is still prose: a text block with no provenance adds nothing.
func TestUntypedTextBlockIsNotEvidence(t *testing.T) {
	f := Finding{
		Title: "SSH banner", Summary: "Banner is old", Target: "tcp://example.com:22",
		Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary",
		Fix: "Upgrade", Retest: "Banner is fixed", Confidence: "certain", Cvss: testCVSS4,
		ProofReview: FindingProofReview{Execution: "demonstrated"},
		Targets:     FindingTargets{{URL: "tcp://example.com:22"}},
		Blocks: []FindingBlock{
			{Type: "text", Role: "action", MD: "Ran nc", Proof: "Connected"},
			{Type: "text", Role: "result", MD: "SSH-2.0-OpenSSH_7.2", Proof: "Old banner"},
			{Type: "text", Role: "control", MD: "Patched host", Proof: "New banner"},
		},
	}
	r := f.ReadinessSummary()
	for _, role := range []string{"action", "result", "control"} {
		if !slices.Contains(r.Gaps, role) {
			t.Errorf("an unprovenanced text block must not satisfy %s: %+v", role, r.Gaps)
		}
	}
}

// A generated render of recorded data is a drawing, not an observation.
func TestGeneratedImageIsNotEvidence(t *testing.T) {
	f := Finding{
		Title: "SSH banner", Summary: "Banner is old", Target: "tcp://example.com:22",
		Severity: "Critical", Status: "verified", Impact: "Bounded observed impact", Why: "Expected boundary",
		Fix: "Upgrade", Retest: "Banner is fixed", Confidence: "certain", Cvss: testCVSS4,
		ProofReview: FindingProofReview{Execution: "demonstrated"},
		Targets:     FindingTargets{{URL: "tcp://example.com:22", ImageHashes: []string{"render-1"}}},
		Blocks: []FindingBlock{
			{Type: "image", Hash: "render-1", Role: "result", Source: "flow_preview", Proof: "Rendered transcript"},
		},
	}
	r := f.ReadinessSummary()
	if !slices.Contains(r.Gaps, "result") || !slices.Contains(r.Gaps, "target_evidence") {
		t.Fatalf("a generated preview must not evidence a role or target: %+v", r.Gaps)
	}
}
