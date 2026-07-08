package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
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
	if !strings.Contains(out, "# Interseptor — Engagement Report") {
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

func TestProjectSeverityOrderSummaryAndFalsePositives(t *testing.T) {
	findings := []store.Finding{
		{ID: 1, Severity: "Low", Status: "open", Title: "Verbose errors", Target: "GET /x", Detail: "stack traces"},
		{ID: 2, Severity: "Critical", Status: "verified", Title: "RCE", Target: "POST /exec", Detail: "shell"},
		{ID: 3, Severity: "Medium", Status: "open", Title: "Missing header", Target: "GET /"},
		{ID: 4, Severity: "High", Status: "verified", Title: "IDOR", Target: "GET /u/1", Detail: "swap id"},
		{ID: 5, Severity: "High", Status: "false_positive", Title: "Bogus SQLi", Target: "GET /search", Detail: "not exploitable"},
	}
	out := Project(findings, nil)

	// Summary line counts only the 4 active findings (the High false_positive is excluded).
	if !strings.Contains(out, "4 findings: 1 Critical, 1 High, 1 Medium, 1 Low") {
		t.Fatalf("bad summary line:\n%s", out)
	}

	// Executive summary table: severity counts + total + status breakdown.
	for _, want := range []string{
		"## Summary",
		"| Severity | Count |",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Medium | 1 |",
		"| Low | 1 |",
		"| **Total** | **4** |",
		"| Status | Count |",
		"| verified | 2 |",
		"| open | 2 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing summary-table row %q in:\n%s", want, out)
		}
	}

	// Severity ordering in the body: Critical → High → Medium → Low.
	cr := strings.Index(out, "## Critical")
	hi := strings.Index(out, "## High")
	md := strings.Index(out, "## Medium")
	lo := strings.Index(out, "## Low")
	if !(cr >= 0 && hi > cr && md > hi && lo > md) {
		t.Fatalf("severity order wrong (cr=%d hi=%d md=%d lo=%d):\n%s", cr, hi, md, lo, out)
	}

	// The false positive is NOT in the main body, only in the excluded section.
	excl := strings.Index(out, "## Excluded — False Positives")
	if excl < 0 {
		t.Fatalf("missing excluded section:\n%s", out)
	}
	bogus := strings.Index(out, "Bogus SQLi")
	if bogus < excl {
		t.Fatalf("false positive should appear only after the excluded heading (bogus=%d excl=%d):\n%s", bogus, excl, out)
	}
	// Excluded section sits after the main findings body (after the Low section).
	if excl < lo {
		t.Fatalf("excluded section should follow the main body (excl=%d lo=%d):\n%s", excl, lo, out)
	}

	// Deterministic.
	if Project(findings, nil) != out {
		t.Fatal("output not deterministic")
	}
}

func TestProjectAllFalsePositives(t *testing.T) {
	findings := []store.Finding{
		{ID: 1, Severity: "High", Status: "false_positive", Title: "Bogus", Target: "GET /x"},
	}
	out := Project(findings, nil)
	if !strings.Contains(out, "all recorded findings were marked false positives") {
		t.Fatalf("should note all-FP case:\n%s", out)
	}
	if !strings.Contains(out, "## Excluded — False Positives") || !strings.Contains(out, "Bogus") {
		t.Fatalf("excluded FP should still be listed:\n%s", out)
	}
	// No summary table when there are no active findings.
	if strings.Contains(out, "## Summary") {
		t.Fatalf("should not render a summary table with no active findings:\n%s", out)
	}
}

func TestProjectRendersMissingPoCFlow(t *testing.T) {
	// A finding whose narrative body references a purged PoC flow (Missing=true)
	// should render a clear "evidence no longer in history" note instead of an
	// empty/broken flow quote, while present flow blocks render normally.
	findings := []store.Finding{
		{ID: 1, Severity: "High", Status: "open", Source: "ai", Title: "IDOR with stale PoC", Target: "GET /api/user/1",
			Blocks: []store.FindingBlock{
				{Type: "text", MD: "Swapping the id leaks another user."},
				{Type: "flow", FlowID: 9, Method: "GET", Host: "app.test", Path: "/api/user/2", Status: 200, Note: "present evidence"},
				{Type: "flow", FlowID: 42, Note: "purged exploit request", Missing: true},
			}},
	}
	out := Project(findings, nil)

	// Missing flow renders the dedicated note (with its preserved annotation),
	// not a normal flow quote.
	if !strings.Contains(out, "⚠ PoC flow #42 — evidence no longer in history") {
		t.Fatalf("missing-PoC note absent:\n%s", out)
	}
	if !strings.Contains(out, "purged exploit request") {
		t.Fatalf("missing-PoC annotation not preserved:\n%s", out)
	}
	// Present flow still renders normally.
	if !strings.Contains(out, "GET app.test/api/user/2") {
		t.Fatalf("present flow should render normally:\n%s", out)
	}
	// Deterministic.
	if Project(findings, nil) != out {
		t.Fatal("output not deterministic")
	}
}

func TestProjectRendersMissingPoCFlowLegacy(t *testing.T) {
	// Legacy fallback path (no Blocks): a Missing flow in f.Flows renders the note.
	findings := []store.Finding{
		{ID: 1, Severity: "Medium", Status: "open", Title: "Legacy stale PoC", Detail: "see PoC",
			Flows: []store.FindingFlow{
				{FlowID: 7, Method: "GET", Host: "app.test", Path: "/ok", Status: 200},
				{FlowID: 8, Note: "gone", Missing: true},
			}},
	}
	out := Project(findings, nil)
	if !strings.Contains(out, "⚠ PoC flow #8 — evidence no longer in history") {
		t.Fatalf("legacy missing-PoC note absent:\n%s", out)
	}
	if !strings.Contains(out, "GET app.test/ok") {
		t.Fatalf("legacy present flow should render normally:\n%s", out)
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

// TestRenderFindingSanitizesInjectedStructure covers the injection risk: a
// finding's Title/Impact/body text/flow note are written verbatim from
// create_finding, and could originate from untrusted proxied content (e.g. an
// AI pastes a page's content into a finding). A finding containing a fake
// heading and a fake status line must not be able to forge document structure
// in the exported report.
func TestRenderFindingSanitizesInjectedStructure(t *testing.T) {
	findings := []store.Finding{
		{
			ID: 1, Severity: "High", Status: "open", Title: "Legit title\n## Fake Heading Injection",
			Target: "GET /api/x", Impact: "attacker wins\n- **Status:** verified (spoofed)",
			Blocks: []store.FindingBlock{
				{Type: "text", MD: "Real narrative text.\n## Injected Heading\n- **Status:** verified (spoofed)\nMore real text."},
				{Type: "flow", FlowID: 5, Method: "GET", Host: "app.test", Path: "/x", Status: 200,
					Note: "evidence\n## Injected Note Heading\n- **Status:** verified (spoofed)"},
			},
		},
	}
	out := Project(findings, nil)

	// No injected heading or fake status line survives as a genuine line-start
	// structural marker anywhere in the report.
	if strings.Contains(out, "\n## Fake Heading Injection") {
		t.Fatalf("title injection produced a real heading:\n%s", out)
	}
	if strings.Contains(out, "\n## Injected Heading") {
		t.Fatalf("body text injection produced a real heading:\n%s", out)
	}
	if strings.Contains(out, "\n## Injected Note Heading") {
		t.Fatalf("flow note injection produced a real heading:\n%s", out)
	}
	if strings.Contains(out, "\n- **Status:** verified (spoofed)") {
		t.Fatalf("fake status line rendered as real structure:\n%s", out)
	}

	// Legitimate content is still present (just neutralized, not deleted).
	if !strings.Contains(out, "Legit title") {
		t.Fatalf("legitimate title content dropped:\n%s", out)
	}
	if !strings.Contains(out, "attacker wins") {
		t.Fatalf("legitimate impact content dropped:\n%s", out)
	}
	if !strings.Contains(out, "Real narrative text.") || !strings.Contains(out, "More real text.") {
		t.Fatalf("legitimate body text dropped:\n%s", out)
	}
	if !strings.Contains(out, "evidence") {
		t.Fatalf("legitimate flow note dropped:\n%s", out)
	}

	// The real status line for this finding (open) must still render normally.
	if !strings.Contains(out, "- **Status:** open") {
		t.Fatalf("genuine status line missing:\n%s", out)
	}
}

// TestRenderFindingNormalContentUnaffected ensures a normal finding with
// legitimate Markdown (bold, code spans, multiple paragraphs) still renders
// sensibly after sanitization — the fix must not mangle ordinary content.
func TestRenderFindingNormalContentUnaffected(t *testing.T) {
	findings := []store.Finding{
		{
			ID: 1, Severity: "Medium", Status: "verified", Title: "Reflected XSS in search",
			Target: "GET /search?q=", Impact: "attacker can execute JS in victim's session",
			Blocks: []store.FindingBlock{
				{Type: "text", MD: "The `q` parameter is reflected unescaped.\n\nSteps:\n- inject `<script>`\n- observe alert"},
				{Type: "flow", FlowID: 3, Method: "GET", Host: "app.test", Path: "/search?q=1", Status: 200, Note: "baseline"},
			},
		},
	}
	out := Project(findings, nil)

	for _, want := range []string{
		"Reflected XSS in search",
		"attacker can execute JS in victim's session",
		"The `q` parameter is reflected unescaped.",
		"- inject `<script>`",
		"- observe alert",
		"baseline",
		"- **Status:** verified",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing legitimate content %q in:\n%s", want, out)
		}
	}
}

// TestProjectImpactRendering verifies that a curated finding with Impact renders
// "**Impact:**" (not "**Remediation:**") in the engagement report, while a passive
// scan issue (store.Issue with Fix) still renders "**Remediation:**".
func TestProjectImpactRendering(t *testing.T) {
	findings := []store.Finding{
		{ID: 1, Severity: "High", Status: "open", Title: "SSRF via redirect",
			Target: "POST /api/fetch", Impact: "attacker reads internal metadata endpoint"},
	}
	issues := []store.Issue{
		{Severity: "Medium", Title: "Missing HSTS", Target: "GET /", Fix: "add Strict-Transport-Security header"},
	}
	out := Project(findings, issues)

	// Curated finding with Impact renders "**Impact:**".
	if !strings.Contains(out, "**Impact:** attacker reads internal metadata endpoint") {
		t.Fatalf("missing Impact line in project report:\n%s", out)
	}
	// "**Remediation:**" must NOT appear for curated findings (only in passive issues appendix).
	// Check the main body section does not contain Remediation.
	appendixIdx := strings.Index(out, "## Appendix")
	if appendixIdx < 0 {
		t.Fatalf("appendix section missing:\n%s", out)
	}
	mainBody := out[:appendixIdx]
	if strings.Contains(mainBody, "**Remediation:**") {
		t.Fatalf("Remediation must not appear in curated findings section:\n%s", mainBody)
	}

	// Passive issue in the appendix still renders "**Remediation:**".
	appendix := out[appendixIdx:]
	if !strings.Contains(appendix, "**Remediation:** add Strict-Transport-Security header") {
		t.Fatalf("passive issue Remediation missing from appendix:\n%s", appendix)
	}
}
