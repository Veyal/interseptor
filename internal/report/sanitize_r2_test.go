package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// findingCard returns the HTML of the n-th (0-based) finding card.
func findingCard(t *testing.T, html string, n int) string {
	t.Helper()
	parts := strings.Split(html, `<section class="finding`)
	if len(parts) <= n+1 {
		t.Fatalf("report has %d finding cards, want at least %d", len(parts)-1, n+1)
	}
	card := parts[n+1]
	end := strings.Index(card, "</section>")
	if end < 0 {
		t.Fatalf("finding card %d is not closed", n)
	}
	return card[:end]
}

var ruleLines = []string{"---", "***", "___", "----", "- - -", "* * *", "_ _ _", "  ---", "\t---  ", "\u00a0---", "---\u2003", "-\t-\t-"}

// A horizontal rule ends a finding card in the HTML/PDF report, so a rule line
// inside finding text used to push the rest of that finding (later proof and
// the remediation) out of its card, where it reads as report-level content.
func TestHTMLReportKeepsFindingCardAcrossRuleLines(t *testing.T) {
	baseline := strings.Count(ProjectHTML([]store.Finding{{ID: 1, Severity: "Low", Status: "open", Title: "Real finding", Fix: "FIX-MARKER", Detail: "plain"}}), "<hr>")
	for _, rule := range ruleLines {
		md := "before\n\n" + rule + "\n\nAFTER-RULE"
		cases := map[string]store.Finding{
			"text block": {Blocks: []store.FindingBlock{{Type: "text", MD: md}}},
			"detail":     {Detail: md},
			"evidence":   {Evidence: md},
			"flow note":  {Blocks: []store.FindingBlock{{Type: "flow", FlowID: 7, Method: "GET", Host: "example.com", Path: "/", Status: 200, Note: md}}},
			"lost flow":  {Blocks: []store.FindingBlock{{Type: "flow", FlowID: 7, Missing: true, Note: md}}},
		}
		for name, f := range cases {
			f.ID, f.Severity, f.Status, f.Title, f.Fix = 1, "Low", "open", "Real finding", "FIX-MARKER"
			html := ProjectHTML([]store.Finding{f})
			if got := strings.Count(html, "<hr>"); got != baseline {
				t.Errorf("%s, rule %q: report has %d <hr>, want %d", name, rule, got, baseline)
			}
			card := findingCard(t, html, 0)
			if !strings.Contains(card, "AFTER-RULE") || !strings.Contains(card, "FIX-MARKER") {
				t.Errorf("%s, rule %q: text after the rule left the finding card", name, rule)
			}
		}
	}
}

// Jira renders a line of four dashes as a rule; the Markdown rule becomes one.
func TestJiraExportHasNoRuleFromFindingText(t *testing.T) {
	for _, rule := range ruleLines {
		f := store.Finding{ID: 1, Severity: "Low", Status: "open", Title: "Real finding",
			Blocks: []store.FindingBlock{{Type: "text", MD: "before\n\n" + rule + "\n\nafter"}}}
		for _, line := range strings.Split(Jira(f, nil).Fields.Description, "\n") {
			if trim := strings.TrimSpace(line); len(trim) >= 3 && strings.Trim(trim, "-") == "" {
				t.Errorf("rule %q: Jira description contains the rule line %q", rule, line)
			}
		}
	}
}

// A line of "=" or "-" under a text line is a setext heading in Markdown: the
// exported .md (and a tracker that renders Markdown) would show the line above
// it as a document heading.
func TestSanitizeBodyNeutralisesSetextUnderlines(t *testing.T) {
	for in, want := range map[string]string{
		"Critical\n===":          "Critical\n\\===",
		"Critical\n=":            "Critical\n\\=",
		"9. Forged finding\n---": "9. Forged finding\n\\---",
		"Forged\n-":              "Forged\n\\-",
		"Forged\n  ==  ":         "Forged\n  \\==  ",
		"a = b\n\n=\nnot under":  "a = b\n\n=\nnot under",
		"- item\n- other":        "- item\n- other",
		"a\n\n-\n":               "a\n\n-\n",
	} {
		if got := sanitizeBody(in); got != want {
			t.Errorf("sanitizeBody(%q) = %q, want %q", in, got, want)
		}
	}
	md := Project([]store.Finding{{ID: 1, Severity: "Low", Status: "open", Title: "Real finding", Detail: "Critical\n===\n\nForged\n---"}})
	for _, line := range strings.Split(md, "\n") {
		if trim := strings.TrimSpace(line); trim != "" && (strings.Trim(trim, "=") == "" || trim == "---") {
			t.Errorf("Markdown report contains the underline %q", line)
		}
	}
}

// Code in a fence is evidence and stays byte-for-byte: a YAML document marker
// or a Markdown table rule inside a fence is not report structure.
func TestSanitizeBodyLeavesRuleLinesInsideFences(t *testing.T) {
	in := "config:\n\n```yaml\n---\nkey: value\n===\n***\n```\n\nafter"
	if got := sanitizeBody(in); got != in {
		t.Fatalf("sanitizeBody changed fenced code:\n got %q\nwant %q", got, in)
	}
	html := ProjectHTML([]store.Finding{{ID: 1, Severity: "Low", Status: "open", Title: "Real finding", Detail: in}})
	if !strings.Contains(html, "<pre><code>---\nkey: value\n===\n***</code></pre>") {
		t.Fatalf("fenced code was altered in the HTML report")
	}
}

// A code fence opened in finding text and never closed used to swallow the rest
// of the document: every later finding rendered as preformatted text inside
// the first finding, with no card, heading or severity.
func TestHTMLReportClosesFenceLeftOpenInFindingText(t *testing.T) {
	open := "see\n\n```\nGET / HTTP/1.1"
	cases := map[string]store.Finding{
		"text block":       {Blocks: []store.FindingBlock{{Type: "text", MD: open}}},
		"detail":           {Detail: open},
		"evidence":         {Evidence: open},
		"evidence fence":   {Evidence: "```\ncode\n```"},
		"flow note":        {Blocks: []store.FindingBlock{{Type: "flow", FlowID: 7, Method: "GET", Host: "example.com", Path: "/", Status: 200, Note: open}}},
		"longer fence":     {Detail: "````\n```\nstill code"},
		"closed then open": {Detail: "```\na\n```\n\n```\nb"},
	}
	for name, first := range cases {
		first.ID, first.Severity, first.Status, first.Title, first.Fix = 1, "Low", "open", "First finding", "FIX-ONE"
		second := store.Finding{ID: 2, Severity: "Low", Status: "open", Title: "Second finding", Fix: "FIX-TWO"}
		html := ProjectHTML([]store.Finding{first, second})
		if n := strings.Count(html, `<section class="finding`); n != 2 {
			t.Errorf("%s: report has %d finding cards, want 2", name, n)
			continue
		}
		if !strings.Contains(findingCard(t, html, 0), "FIX-ONE") || !strings.Contains(findingCard(t, html, 1), "FIX-TWO") {
			t.Errorf("%s: a remediation is not inside its own finding card", name)
		}
		desc := Jira(first, nil).Fields.Description
		if strings.Count(desc, "{noformat}")%2 != 0 {
			t.Errorf("%s: Jira description leaves a {noformat} block open", name)
		}
	}
}

// Balanced fences and ordinary text are returned unchanged.
func TestSanitizeBodyKeepsBalancedFences(t *testing.T) {
	for _, in := range []string{"", "plain", "```\ncode\n```", "```http\nGET /\n```\ntail\n", "a `code` span and ``` inline", "````\n```\n````"} {
		if got := sanitizeBody(in); got != in {
			t.Errorf("sanitizeBody(%q) = %q, want it unchanged", in, got)
		}
	}
}

// Text that continues a report line ("**Evidence:** …", a flow note) is not at
// a line start on its first line, so that line is left as written; the lines
// after it are.
func TestSanitizeBodyInlineFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"---":               "---",
		"```":               "```",
		"---\n---":          "---\n\\---",
		"\n===":             "\n\\===",
		"note\n```\ncode":   "note\n```\ncode\n```",
		"plain note":        "plain note",
		"```\ncode\n```":    "```\ncode\n```\n```",
		"# x\n\n- **y:** z": "\\# x\n\n- \\**y:** z",
	} {
		if got := sanitizeBodyInline(in); got != want {
			t.Errorf("sanitizeBodyInline(%q) = %q, want %q", in, got, want)
		}
	}
}
