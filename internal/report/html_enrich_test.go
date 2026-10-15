package report

import (
	"strings"
	"testing"
)

// enrich runs the production pipeline projectHTMLDoc uses on report Markdown.
func enrich(md string) string {
	return strings.Join(enrichReport(markdownBlocks(md)), "\n")
}

func TestEnrichBuildsSeverityFindingCard(t *testing.T) {
	md := `# Interseptor — Engagement Report

## High

### 1. SQL injection in login

- **Status:** open
- **Confidence:** firm
- **CVSS:** CVSS:4.0/AV:N/AC:L
- **Calculated CVSS v4.0:** 8.7 (HIGH, CVSS-B)
- **CWE:** CWE-89
- **Tags:** api, app

**Summary:** Filter values are concatenated into SQL.
`
	got := enrich(md)
	for _, want := range []string{
		`<section class="finding sev-high">`,
		`<span class="finding-id">1</span>`,
		`<h3 class="finding-title">SQL injection in login</h3>`,
		`<span class="sev-tag">High</span>`,
		`<span class="score-tag" title="CVSS v4.0 base score">8.7</span>`,
		`<div class="finding-meta">`,
		`m-badge status st-open`,
		`<span class="m-v">open</span>`,
		`<code class="m-vec">CVSS:4.0/AV:N/AC:L</code>`,
		`<span class="m-tag">api</span>`,
		`<span class="m-tag">app</span>`,
		`<strong>Summary:</strong>`,
		`<h2 class="sevhead sev-high">High</h2>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The h1 title must stay a bare tag (pinned by ProjectHTML tests).
	if !strings.Contains(got, "<h1>") {
		t.Error("h1 lost its bare tag")
	}
	// The score line must not also survive as a raw metadata badge/row.
	if strings.Contains(got, "Calculated CVSS v4.0") {
		t.Errorf("calculated CVSS leaked into the meta band:\n%s", got)
	}
}

func TestEnrichDerivesSeverityFromCVSSWhenGroupedByTag(t *testing.T) {
	md := `## Tag: api

### 1. Reflected XSS

- **Status:** verified
- **Calculated CVSS v4.0:** 6.1 (MEDIUM, CVSS-B)
`
	got := enrich(md)
	for _, want := range []string{
		`<section class="finding sev-medium">`,
		`<span class="sev-tag">Medium</span>`,
		`<span class="score-tag" title="CVSS v4.0 base score">6.1</span>`,
		`m-badge status st-verified`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// "## Tag: api" is not a severity, so it is left as a plain section header.
	if strings.Contains(got, "sevhead") {
		t.Errorf("a tag header was coloured as a severity:\n%s", got)
	}
}

func TestEnrichDoesNotBoxTheEngagementContextSubHead(t *testing.T) {
	md := `## Engagement context

### How evidence was obtained

Interception setup v1.
`
	got := enrich(md)
	if strings.Contains(got, `class="finding`) {
		t.Errorf("the context sub-head was boxed as a finding:\n%s", got)
	}
	if !strings.Contains(got, "<h3>How evidence was obtained</h3>") {
		t.Errorf("context sub-head lost its plain heading:\n%s", got)
	}
}

func TestEnrichFindingWithoutMetadataFallsBackToGroupSeverity(t *testing.T) {
	md := `## Low

### 1. Verbose error page

**Summary:** Stack traces are shown to anonymous users.
`
	got := enrich(md)
	if !strings.Contains(got, `<section class="finding sev-low">`) {
		t.Errorf("group severity not applied without metadata:\n%s", got)
	}
	if strings.Contains(got, `<div class="finding-meta">`) {
		t.Errorf("an empty meta band was emitted:\n%s", got)
	}
	if !strings.Contains(got, `<span class="sev-tag">Low</span>`) {
		t.Errorf("severity tag missing:\n%s", got)
	}
}

func TestEnrichClosesEachFindingBeforeTheNext(t *testing.T) {
	md := `## Medium

### 1. First

- **Status:** open

### 2. Second

- **Status:** open
`
	got := enrich(md)
	if n := strings.Count(got, `<section class="finding`); n != 2 {
		t.Errorf("want 2 finding cards, got %d:\n%s", n, got)
	}
	if o, c := strings.Count(got, "<section"), strings.Count(got, "</section>"); o != c {
		t.Errorf("unbalanced sections: %d open, %d close", o, c)
	}
	if o, c := strings.Count(got, "<div"), strings.Count(got, "</div>"); o != c {
		t.Errorf("unbalanced divs: %d open, %d close", o, c)
	}
}
