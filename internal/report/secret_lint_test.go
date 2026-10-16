package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const reportJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJlZXhhbXBsZQ"

func TestProjectHTMLSecretLintNoticeNeverEchoesValue(t *testing.T) {
	findings := []store.Finding{{
		ID: 7, Severity: "Medium", Status: "verified", Title: "Token <b>in URL</b>",
		Summary: "The session token " + reportJWT + " is sent in the query string.",
		Blocks:  []store.FindingBlock{{Type: "text", MD: "Header Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345"}},
	}}
	for name, html := range map[string]string{
		"flat":    ProjectHTML(findings, nil),
		"grouped": ProjectHTMLGroupedByTag(findings, nil, nil, nil),
	} {
		if !strings.Contains(html, `class="secret-lint"`) || !strings.Contains(html, "#7") || !strings.Contains(html, "[redacted jwt") || !strings.Contains(html, "bearer_token") {
			t.Fatalf("%s: lint notice missing or incomplete:\n%s", name, html)
		}
		lint := html[strings.Index(html, `class="secret-lint"`):]
		lint = lint[:strings.Index(lint, "</aside>")]
		if strings.Contains(lint, reportJWT) || strings.Contains(lint, "abcdefghijklmnopqrstuvwxyz012345") {
			t.Fatalf("%s: notice leaked the value: %s", name, lint)
		}
		if strings.Contains(lint, "<b>") {
			t.Fatalf("%s: notice must escape finding titles: %s", name, lint)
		}
	}
}

func TestProjectHTMLNoSecretLintNoticeForCleanFindings(t *testing.T) {
	html := ProjectHTML([]store.Finding{{ID: 1, Severity: "Low", Status: "verified", Title: "Clean", Summary: "Token [redacted jwt len=71 sha256=0123456789ab]."}}, nil)
	if strings.Contains(html, `class="secret-lint"`) {
		t.Fatal("clean findings must not render the notice")
	}
}
