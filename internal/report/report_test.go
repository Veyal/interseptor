package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func TestFindingsEmpty(t *testing.T) {
	out := Findings(nil)
	if !strings.Contains(out, "No findings") {
		t.Fatalf("empty report should say so: %s", out)
	}
}

func TestFindingsGroupsAndOrders(t *testing.T) {
	issues := []store.Issue{
		{Severity: "Low", Title: "Cookie weak", Target: "GET a/b", Detail: "d", Evidence: "Set-Cookie: x", Fix: "harden"},
		{Severity: "High", Title: "Token leak", Target: "POST a/login", Detail: "leaked", Evidence: "eyJ...", Fix: "use cookie"},
		{Severity: "Medium", Title: "Missing CSP", Target: "GET a/", Fix: "add csp"},
	}
	out := Findings(issues)

	// Summary line reflects counts.
	if !strings.Contains(out, "3 findings: 1 High, 1 Medium, 1 Low") {
		t.Fatalf("bad summary: %s", out)
	}
	// High section precedes Medium precedes Low.
	hi, md, lo := strings.Index(out, "## High"), strings.Index(out, "## Medium"), strings.Index(out, "## Low")
	if !(hi >= 0 && md > hi && lo > md) {
		t.Fatalf("severity order wrong (hi=%d md=%d lo=%d):\n%s", hi, md, lo, out)
	}
	// Fields render.
	for _, want := range []string{"### 1. Token leak", "- **Target:** `POST a/login`", "- **Remediation:** use cookie", "- **Evidence:** `eyJ...`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// Deterministic.
	if Findings(issues) != out {
		t.Fatal("output not deterministic")
	}
}

func TestFindingsSanitizesEvidence(t *testing.T) {
	out := Findings([]store.Issue{{Severity: "Low", Title: "x", Evidence: "line1\nline2`with`ticks"}})
	if strings.Contains(out, "`with`") || strings.Contains(out, "line1\nline2") {
		t.Fatalf("evidence not sanitized for inline code: %s", out)
	}
}
