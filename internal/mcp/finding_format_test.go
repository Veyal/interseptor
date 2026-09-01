package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateFindingFormatRejectsWallOfText(t *testing.T) {
	wall := strings.Repeat("The application exposes an unauthenticated admin panel that allows full config dump including database credentials and SMTP passwords which an attacker can use. ", 3)
	err, _ := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Detail:   wall,
	})
	if err == nil {
		t.Fatal("expected hard reject for wall-of-text detail without impact/why fields")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "impact") {
		t.Fatalf("error should point at impact/why fields, got: %v", err)
	}
}

func TestValidateFindingFormatAcceptsPointFirst(t *testing.T) {
	body := `[{"type":"text","role":"baseline","md":"Open a record owned by the current account."},{"type":"flow","role":"baseline","flowId":12,"note":"Own account","proof":"Establishes normal authorized access."},{"type":"text","role":"action","md":"Replace the object identifier with another account's example identifier."},{"type":"flow","role":"result","flowId":13,"note":"Cross-account response","proof":"The current session receives another account's record."},{"type":"image","role":"result","hash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","source":"flow_preview","sourceFlowId":13,"caption":"Cross-account response","proof":"Highlights the returned foreign account fields."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Summary:  "A signed-in user can retrieve another account's profile.",
		Impact:   "full PII disclosure",
		Why:      "Broken object-level authorization",
		Target:   "GET api.example.com/users/{id}",
		Fix:      "Enforce object ownership on every profile lookup.",
		Retest:   "Repeat with unrelated accounts and confirm a uniform denial.",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("well-formed finding should pass: %v", err)
	}
	if len(warns) > 0 {
		t.Fatalf("well-formed finding should have no warnings, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsMissingImpact(t *testing.T) {
	body := `[{"type":"flow","flowId":1,"note":"After: leak"}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Why:      "broken authz",
		Target:   "example.com",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(strings.ToLower(joined), "impact") {
		t.Fatalf("expected impact warning, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsHighWithoutFlow(t *testing.T) {
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Critical",
		Impact:   "admin takeover",
		Why:      "default credentials",
		Target:   "admin.example.com",
		Detail:   "Login with admin/admin worked.",
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(strings.ToLower(joined), "flow") && !strings.Contains(joined, "PoC") {
		t.Fatalf("expected flow/PoC warning for Critical, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsNeedsVerificationWithoutInstructions(t *testing.T) {
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Medium",
		Status:   "needs_verification",
		Impact:   "possible PII exposure",
		Why:      "open bucket",
		Target:   "s3.example.com",
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "verificationInstructions") {
		t.Fatalf("expected verificationInstructions warning, got %v", warns)
	}
}

func TestValidateFindingFormatAllowsShortOpening(t *testing.T) {
	err, _ := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Detail:   "IDOR on /api/users/{id} — attaching PoC next.",
	})
	if err != nil {
		t.Fatalf("short opening must not be rejected: %v", err)
	}
}

func TestValidateFindingFormatWarnsCredentialsNotHighlighted(t *testing.T) {
	body := `[{"type":"text","md":"The config contains password=s3cretValue in plaintext."},{"type":"flow","flowId":1,"note":"After: dump"}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Impact:   "DB access",
		Why:      "secrets in cleartext",
		Target:   "nacos",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(strings.ToLower(joined), "credential") && !strings.Contains(strings.ToLower(joined), "bold") {
		t.Fatalf("expected credentials-highlight warning, got %v", warns)
	}
}

func TestMCPInstructionsRequireFindingFormat(t *testing.T) {
	instr := mcpInstructions()
	for _, want := range []string{"REQUIRED FORMAT", "summary", "impact", "why", "target", "retest", "Evidence", "proof", "browser screenshot", "NOT confirmed", "Differential proof"} {
		if !strings.Contains(instr, want) {
			t.Fatalf("mcpInstructions missing %q:\n%s", want, instr)
		}
	}
	for _, forbidden := range []string{"Standard: Before", "add_finding_poc for Before/Action/After", "(text→flow→text)"} {
		if strings.Contains(instr, forbidden) {
			t.Fatalf("Before/Action/After must be a preset, not the universal format (%q):\n%s", forbidden, instr)
		}
	}
}

func TestFindingBlocksSchemaAdvertisesCapturedFlowProvenance(t *testing.T) {
	raw, err := json.Marshal(findingBlocksSchema())
	if err != nil {
		t.Fatalf("marshal finding block schema: %v", err)
	}
	if !strings.Contains(string(raw), "captured_flow") {
		t.Fatalf("finding block schema omits captured-flow provenance: %s", raw)
	}
}

func TestValidateFindingFormatAcceptsTypedObservationEvidence(t *testing.T) {
	body := `[{"type":"text","role":"observation","md":"Request the public configuration endpoint."},{"type":"flow","role":"result","flowId":12,"proof":"The unauthenticated response contains internal service names."},{"type":"image","role":"result","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":"flow_preview","sourceFlowId":12,"caption":"Unauthenticated response","proof":"Visually identifies the exposed internal service list."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Medium",
		Summary:  "An unauthenticated endpoint exposes internal service configuration.",
		Impact:   "An external attacker can map internal services and deployment details.",
		Why:      "Sensitive configuration is returned without authentication.",
		Target:   "GET https://api.example.com/config",
		Fix:      "Require authorization and return only non-sensitive public configuration.",
		Retest:   "Repeat the unauthenticated request and confirm a uniform denial without metadata.",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("typed observation finding should pass: %v", err)
	}
	if len(warns) > 0 {
		t.Fatalf("complete typed finding should have no warnings, got %v", warns)
	}
}

func TestValidateFindingFormatDoesNotRequireDifferentialNarrative(t *testing.T) {
	body := `[{"type":"text","role":"observation","md":"Observe the missing Content-Security-Policy header."},{"type":"flow","role":"result","flowId":9,"proof":"The response headers omit Content-Security-Policy."},{"type":"image","role":"result","hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","source":"flow_preview","sourceFlowId":9,"caption":"Response headers","proof":"Shows the absent policy header."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Low", Summary: "The application omits a browser policy header.",
		Impact: "Defense in depth is reduced.", Why: "The browser receives no content policy.",
		Target: "https://www.example.com", Fix: "Deploy a restrictive policy.",
		Retest: "Confirm the response includes the approved policy.", Body: body,
	})
	if err != nil {
		t.Fatalf("non-differential finding should pass: %v", err)
	}
	joined := strings.ToLower(strings.Join(warns, "\n"))
	if strings.Contains(joined, "before") || strings.Contains(joined, "after") {
		t.Fatalf("non-differential finding must not be told to add Before/After: %v", warns)
	}
}

func TestValidateFindingFormatWarnsEvidenceWithoutProof(t *testing.T) {
	body := `[{"type":"text","role":"action","md":"Send the modified request."},{"type":"flow","role":"result","flowId":7,"note":"Cross-account response"},{"type":"image","role":"result","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","caption":"Returned account"}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Medium", Summary: "Authorization can be bypassed.", Impact: "Another account can be read.",
		Why: "Object ownership is not enforced.", Target: "api.example.com", Body: body,
	})
	if err != nil {
		t.Fatalf("missing proof annotation should warn, not reject: %v", err)
	}
	if !strings.Contains(strings.ToLower(strings.Join(warns, "\n")), "proof") {
		t.Fatalf("expected proof annotation warning, got %v", warns)
	}
}

func TestCreateFindingForwardsWhyAndFormat(t *testing.T) {
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/findings" {
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"title":"t","ready":false}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"IDOR","severity":"High","impact":"PII leak","why":"broken authz","target":"api.example.com","cwe":"CWE-639","environment":"staging"}}}` + "\n"
	var out strings.Builder
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotBody["why"] != "broken authz" || gotBody["impact"] != "PII leak" || gotBody["cwe"] != "CWE-639" {
		t.Fatalf("body = %+v", gotBody)
	}
}
