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
		t.Fatal("expected hard reject for wall-of-text detail without ## headings")
	}
	if !strings.Contains(err.Error(), "## Summary") {
		t.Fatalf("error should point at required format, got: %v", err)
	}
}

func TestValidateFindingFormatAcceptsSectionedBody(t *testing.T) {
	body := `[{"type":"text","md":"## Summary\nAttacker can read all user PII via IDOR on /api/users/{id}.\n\n## Evidence\n- Baseline: flow 12 (own user)\n- Exploit: flow 13 (victim id)\n\n## Impact\nConfidentiality breach of all user records."},{"type":"flow","flowId":13,"note":"exploit"}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Impact:   "full PII disclosure",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("well-formed body should pass: %v", err)
	}
	if len(warns) > 0 {
		t.Fatalf("well-formed body should have no warnings, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsMissingImpact(t *testing.T) {
	body := `[{"type":"text","md":"## Summary\nIDOR on /api/users/{id} returns other users.\n\n## Evidence\nSee attached flow."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "## Impact") && !strings.Contains(strings.ToLower(joined), "impact") {
		t.Fatalf("expected impact warning, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsHighWithoutFlow(t *testing.T) {
	body := `[{"type":"text","md":"## Summary\nAttacker gains admin via default creds.\n\n## Evidence\nLogin with admin/admin worked.\n\n## Impact\nFull admin takeover."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Critical",
		Impact:   "admin takeover",
		Body:     body,
	})
	if err != nil {
		t.Fatalf("should warn not reject: %v", err)
	}
	joined := strings.Join(warns, "\n")
	if !strings.Contains(strings.ToLower(joined), "flow") {
		t.Fatalf("expected flow/PoC warning for Critical, got %v", warns)
	}
}

func TestValidateFindingFormatWarnsNeedsVerificationWithoutInstructions(t *testing.T) {
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Medium",
		Status:   "needs_verification",
		Detail:   "## Summary\nPossible PII in bucket object.\n\n## Impact\nMay expose identity documents.",
		Impact:   "possible PII exposure",
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
	// create_finding often starts with a short what/where; full sections come later.
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Detail:   "IDOR on /api/users/{id} — attaching PoC next.",
	})
	if err != nil {
		t.Fatalf("short opening must not be rejected: %v", err)
	}
	_ = warns // soft warnings about missing impact/sections are fine
}

func TestValidateFindingFormatWarnsCredentialsNotHighlighted(t *testing.T) {
	body := `[{"type":"text","md":"## Summary\nNacos dump leaks DB password.\n\n## Evidence\nThe config contains password=s3cretValue in plaintext.\n\n## Impact\nAttacker can connect to the database."}]`
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "High",
		Impact:   "DB access",
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
	for _, want := range []string{"## Summary", "## Evidence", "## Impact", "REQUIRED FORMAT", "NOT confirmed"} {
		if !strings.Contains(instr, want) {
			t.Fatalf("mcpInstructions missing %q:\n%s", want, instr)
		}
	}
}

func TestMCPInstructionsScopeHumanInputToEngagement(t *testing.T) {
	instr := mcpInstructions()
	for _, want := range []string{
		"HUMAN INPUT (Interseptor / target engagement only)",
		"Do NOT use it for local machine/OS admin",
		"ASK FOR FINDINGS",
		"do not route it through request_human_input",
	} {
		if !strings.Contains(instr, want) {
			t.Fatalf("mcpInstructions missing %q:\n%s", want, instr)
		}
	}
	desc := ""
	for _, tdef := range New("http://127.0.0.1:1").toolList() {
		if name, _ := tdef["name"].(string); name == "request_human_input" {
			desc, _ = tdef["description"].(string)
			break
		}
	}
	if desc == "" {
		t.Fatal("request_human_input tool not registered")
	}
	for _, want := range []string{"Interseptor / target-engagement", "Do NOT use for local OS/admin"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("request_human_input description missing %q:\n%s", want, desc)
		}
	}
}

func TestCreateFindingRejectsWallOfText(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("API must not be called when format validation rejects")
	}))
	defer mock.Close()

	srv := New(mock.URL)
	wall := strings.Repeat("Exposed admin panel dumps credentials and allows remote code execution against the production database. ", 4)
	_, err := srv.Call("create_finding", map[string]any{
		"title":    "Nacos dump",
		"severity": "Critical",
		"detail":   wall,
	})
	if err == nil {
		t.Fatal("expected create_finding to reject wall-of-text")
	}
	if !strings.Contains(err.Error(), "## Summary") {
		t.Fatalf("reject message should cite required format: %v", err)
	}
}

func TestCreateFindingAppendsFormatWarnings(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/findings" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": 9, "title": "t", "severity": "High"})
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	srv := New(mock.URL)
	body := `[{"type":"text","md":"## Summary\nDefault creds on admin panel.\n\n## Evidence\nLogged in with admin/admin."}]`
	out, err := srv.Call("create_finding", map[string]any{
		"title":    "Default creds",
		"severity": "High",
		"body":     body,
	})
	if err != nil {
		t.Fatalf("create_finding: %v", err)
	}
	if !strings.Contains(out, "FORMAT WARNING") {
		t.Fatalf("expected FORMAT WARNING in response, got:\n%s", out)
	}
	if !strings.Contains(out, "/#finding-9") {
		t.Fatalf("UI URL still required:\n%s", out)
	}
}
