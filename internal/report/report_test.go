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

func TestProjectRendersFindingsAndPoCsAndAppendix(t *testing.T) {
	findings := []store.Finding{
		{ID: 2, Severity: "Low", Status: "open", Source: "ai", Title: "Verbose errors", Target: "GET /api/x", Detail: "stack traces", Fix: "hide"},
		{ID: 1, Severity: "High", Status: "verified", Source: "human", Title: "IDOR on user", Target: "GET /api/user/1",
			Detail: "swap id", Evidence: "id=2 leaks", Fix: "authorize",
			Flows: []store.FindingFlow{{FlowID: 7, Method: "GET", Host: "app.test", Path: "/api/user/2", Status: 200, Note: "leaks other user"}}},
	}
	issues := []store.Issue{{Severity: "Medium", Title: "Missing CSP", Target: "GET /"}}
	out := Project(findings, issues)

	// Title + summary counts curated findings.
	if !strings.Contains(out, "# Interceptor — Engagement Report") {
		t.Fatalf("missing title:\n%s", out)
	}
	if !strings.Contains(out, "2 findings: 1 High, 1 Low") {
		t.Fatalf("bad summary:\n%s", out)
	}
	// High precedes Low (severity ordering), regardless of input order.
	hi, lo := strings.Index(out, "## High"), strings.Index(out, "## Low")
	if !(hi >= 0 && lo > hi) {
		t.Fatalf("severity order wrong (hi=%d lo=%d):\n%s", hi, lo, out)
	}
	// Status + PoC flow render under the finding.
	for _, want := range []string{"### 1. IDOR on user", "**Status:** verified", "**PoC flows:**", "GET app.test/api/user/2", "→ 200", "leaks other user"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// Passive-scan appendix is present.
	if !strings.Contains(out, "## Appendix: Passive Scan Issues") || !strings.Contains(out, "Missing CSP") {
		t.Fatalf("missing appendix:\n%s", out)
	}
	// Deterministic.
	if Project(findings, issues) != out {
		t.Fatal("output not deterministic")
	}
}

func TestProjectEmpty(t *testing.T) {
	out := Project(nil, nil)
	if !strings.Contains(out, "No findings recorded") {
		t.Fatalf("empty project report should say so: %s", out)
	}
}

func TestFindingsSanitizesEvidence(t *testing.T) {
	out := Findings([]store.Issue{{Severity: "Low", Title: "x", Evidence: "line1\nline2`with`ticks"}})
	if strings.Contains(out, "`with`") || strings.Contains(out, "line1\nline2") {
		t.Fatalf("evidence not sanitized for inline code: %s", out)
	}
}
