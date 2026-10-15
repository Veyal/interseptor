package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestProjectHTMLContainsStructure(t *testing.T) {
	findings := []store.Finding{
		{ID: 1, Severity: "High", Status: "verified", Title: "IDOR", Detail: "swap id",
			Flows: []store.FindingFlow{{FlowID: 1, Method: "GET", Host: "app.test", Path: "/u/2", Status: 200}}},
	}
	html := ProjectHTML(findings, nil)
	for _, want := range []string{"<!DOCTYPE html>", "<h1>", "IDOR", "GET app.test/u/2", "<table>", "</html>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in html", want)
		}
	}
	if strings.Contains(html, "Appendix: Passive Scan") {
		t.Fatal("passive appendix should not appear when issues nil")
	}
}

func TestProjectHTMLWithAppendix(t *testing.T) {
	issues := []store.Issue{{Severity: "Medium", Title: "Missing CSP", Target: "GET /"}}
	html := ProjectHTML(nil, issues)
	if !strings.Contains(html, "Missing CSP") {
		t.Fatalf("appendix missing:\n%s", html)
	}
}

func TestMarkdownToHTMLRequiresMatchingFenceLength(t *testing.T) {
	html := markdownToHTML("````http\nHTTP/1.1 200 OK\n```\npayload continues\n````\n")
	if strings.Count(html, "<pre><code>") != 1 || !strings.Contains(html, "HTTP/1.1 200 OK\n```\npayload continues") {
		t.Fatalf("long fence closed by shorter payload delimiter: %s", html)
	}
	if strings.Contains(html, "<p>payload continues</p>") {
		t.Fatalf("payload escaped the code fence: %s", html)
	}
}

// Every evidence provenance line the report emits is wrapped in underscores and
// also contains an underscore inside ("source=captured_flow"), so an emphasis
// rule that only fires on exactly two underscores left the delimiters visible in
// the HTML and PDF deliverable.
func TestMarkdownToHTMLEmphasisesLinesContainingUnderscores(t *testing.T) {
	html := markdownToHTML("_Evidence: role=action, source=captured_flow._")
	if !strings.Contains(html, "<em>Evidence: role=action, source=captured_flow.</em>") {
		t.Fatalf("provenance line not emphasised: %s", html)
	}
	if strings.Contains(html, ">_Evidence") || strings.Contains(html, "flow._<") {
		t.Fatalf("underscore delimiters leaked into the output: %s", html)
	}
	// A plain sentence that merely contains an underscore is left alone.
	if got := markdownToHTML("source=captured_flow is the provenance"); strings.Contains(got, "<em>") {
		t.Fatalf("emphasised a line with no delimiters: %s", got)
	}
	// Markdown's own rule: a space after the opening delimiter is not emphasis.
	if got := markdownToHTML("_ not emphasis _"); strings.Contains(got, "<em>") {
		t.Fatalf("emphasised a padded delimiter: %s", got)
	}
}
