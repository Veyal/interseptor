package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// credentialFixture is a finding whose attached evidence carries every header
// the mask pass must cover, in both the canonical block and the legacy flow
// list. The values are invented and use example.com only.
func credentialFixture() []store.Finding {
	req := "GET /invoices/8 HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Authorization: Bearer tok-aaaabbbbccccdddd\r\n" +
		"Cookie: sid=cookie-1111222233334444\r\n" +
		"Proxy-Authorization: Basic cHJveHk6c2VjcmV0dmFsdWU=\r\n" +
		"\r\n"
	res := "HTTP/1.1 200 OK\r\n" +
		"Set-Cookie: sid=rotated-5555666677778888; HttpOnly\r\n" +
		"Content-Type: application/json\r\n" +
		"\r\n" +
		`{"invoice":"8","tenant":"other"}`
	return []store.Finding{{
		ID: 1, Severity: "High", Status: "verified", Title: "IDOR on /invoices/{id}",
		Blocks: []store.FindingBlock{{
			Type: "flow", FlowID: 7, Role: "action", Proof: "Reads another tenant's invoice.",
			Source: "captured_flow", Method: "GET", Host: "example.com", Path: "/invoices/8",
			Status: 200, ReqRaw: req, ResRaw: res,
		}},
		Flows: []store.FindingFlow{{
			FlowID: 7, Method: "GET", Host: "example.com", Path: "/invoices/8", Status: 200,
			ReqRaw: req, ResRaw: res,
		}},
	}}
}

var fixtureSecrets = []string{
	"tok-aaaabbbbccccdddd",
	"cookie-1111222233334444",
	"cHJveHk6c2VjcmV0dmFsdWU=",
	"rotated-5555666677778888",
}

// The default export is evidence, verbatim. Masking must be opt-in, so this
// test fails the moment a mask pass is wired into the default render path.
func TestProjectKeepsCredentialHeadersVerbatimByDefault(t *testing.T) {
	md := Project(credentialFixture(), nil)
	for _, secret := range fixtureSecrets {
		if !strings.Contains(md, secret) {
			t.Errorf("default export dropped %q — masking must be opt-in", secret)
		}
	}
	if strings.Contains(md, "[redacted") {
		t.Error("default export must not contain a placeholder")
	}
	if ProjectHTML(credentialFixture(), nil) != HTMLFromMarkdown(md, credentialFixture()) {
		t.Error("HTML render drifted from the Markdown source")
	}
}

func TestMaskSensitiveHeadersLeavesNothingRecoverable(t *testing.T) {
	masked := MaskSensitiveHeaders(credentialFixture())
	for _, out := range []string{Project(masked, nil), ProjectHTML(masked, nil), ProjectGroupedByTag(masked, nil, nil, nil)} {
		for _, secret := range fixtureSecrets {
			if strings.Contains(out, secret) {
				t.Errorf("masked export leaked %q", secret)
			}
		}
		if !strings.Contains(out, "[redacted") {
			t.Error("masked export has no placeholder")
		}
		// Evidence that is not a credential must survive the pass.
		for _, keep := range []string{"GET /invoices/8 HTTP/1.1", "Content-Type: application/json", "invoice"} {
			if !strings.Contains(out, keep) {
				t.Errorf("masked export dropped non-credential evidence %q", keep)
			}
		}
	}
}

func TestMaskSensitiveHeadersDoesNotMutateInput(t *testing.T) {
	in := credentialFixture()
	before := Project(in, nil)
	_ = MaskSensitiveHeaders(in)
	if after := Project(in, nil); after != before {
		t.Fatal("MaskSensitiveHeaders mutated the caller's findings")
	}
}

func TestMaskSensitiveHeadersKeepsProvenanceAndStructure(t *testing.T) {
	masked := MaskSensitiveHeaders(credentialFixture())
	if len(masked) != 1 || len(masked[0].Blocks) != 1 {
		t.Fatalf("structure changed: %+v", masked)
	}
	bl := masked[0].Blocks[0]
	if bl.Role != "action" || bl.Source != "captured_flow" || bl.Proof == "" || bl.FlowID != 7 {
		t.Fatalf("provenance grading lost: %+v", bl)
	}
	md := Project(masked, nil)
	for _, want := range []string{"role=action", "source=captured_flow", "IDOR on /invoices/{id}"} {
		if !strings.Contains(md, want) {
			t.Errorf("masked report lost %q", want)
		}
	}
}

func TestMaskSensitiveHeadersIsIdempotentAndHandlesEmpty(t *testing.T) {
	if got := MaskSensitiveHeaders(nil); got != nil {
		t.Fatalf("nil input: %+v", got)
	}
	once := Project(MaskSensitiveHeaders(credentialFixture()), nil)
	twice := Project(MaskSensitiveHeaders(MaskSensitiveHeaders(credentialFixture())), nil)
	if once != twice {
		t.Fatal("mask pass is not idempotent")
	}
}
