package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A table row whose cell happens to contain "---" (a shell flag, a URL path, a
// slug) is a data row, not the table's delimiter row. The old check
// (strings.Contains(row, "---")) dropped it from the HTML/PDF report and from the
// Jira ticket, silently losing a line of evidence.
const tableWithDashedCell = "| Field | Value |\n| --- | --- |\n| slug | user---account |\n| flag | --only----x |\n"

func TestMarkdownTableKeepsRowsWhoseCellContainsDashes(t *testing.T) {
	html := markdownToHTML(tableWithDashedCell)
	if !strings.Contains(html, "user---account") {
		t.Fatalf("data row with --- in a cell was dropped: %s", html)
	}
	if !strings.Contains(html, "--only----x") {
		t.Fatalf("data row with a run of dashes was dropped: %s", html)
	}
	// The delimiter row itself must still be dropped, and the header must stay a header.
	if strings.Contains(html, "<td>---</td>") {
		t.Fatalf("delimiter row rendered as data: %s", html)
	}
	if !strings.Contains(html, "<th>Field</th>") || strings.Count(html, "<tr>") != 3 {
		t.Fatalf("want one header and two data rows: %s", html)
	}
}

func TestJiraTableKeepsRowsWhoseCellContainsDashes(t *testing.T) {
	wiki := markdownToJiraWiki(tableWithDashedCell)
	if !strings.Contains(wiki, "user---account") || !strings.Contains(wiki, "--only----x") {
		t.Fatalf("jira table dropped a row with dashes: %s", wiki)
	}
	if !strings.HasPrefix(wiki, "||Field||Value||") {
		t.Fatalf("header row should be a Jira header: %s", wiki)
	}
	if strings.Contains(wiki, "|---") {
		t.Fatalf("delimiter row leaked into the wiki markup: %s", wiki)
	}
}

// isTableSeparator accepts the delimiter rows GFM accepts and nothing else.
func TestIsTableSeparator(t *testing.T) {
	for _, row := range []string{"| --- | --- |", "|---|:--:|", "| :-: | -- |", "|  ---  |"} {
		if !isTableSeparator(row) {
			t.Fatalf("%q should be a delimiter row", row)
		}
	}
	for _, row := range []string{"| --- | x |", "| a---b | c |", "|  |  |", "|---", "|slug|user---account|"} {
		if isTableSeparator(row) {
			t.Fatalf("%q should be a data row", row)
		}
	}
}

// A false-positive finding's title is a single inline field in the appendix, like
// the one in the main body: a newline in it must not forge report structure.
func TestProjectFalsePositiveAppendixNeutralisesTitleAndSeverity(t *testing.T) {
	md := Project([]store.Finding{{
		ID:       4,
		Title:    "Dismissed\n\n## Injected heading\n- **Status:** verified",
		Severity: "Low\n- **Target:** `https://attacker.test`",
		Status:   "false_positive",
		Target:   "GET /x",
	}})
	if strings.Contains(md, "\n## Injected heading") {
		t.Fatalf("false-positive title forged a heading:\n%s", md)
	}
	if strings.Contains(md, "\n- **Target:** `https://attacker.test`") {
		t.Fatalf("false-positive severity forged a metadata bullet:\n%s", md)
	}
	// The main body path already neutralised it; both must agree.
	active := Project([]store.Finding{{ID: 5, Title: "T\n## Injected heading", Severity: "High", Status: "open"}})
	if strings.Contains(active, "\n## Injected heading") {
		t.Fatalf("main body title forged a heading:\n%s", active)
	}
	if !strings.Contains(md, "Dismissed") {
		t.Fatalf("the title text itself must survive:\n%s", md)
	}
}
