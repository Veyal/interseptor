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
