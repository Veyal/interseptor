package control

import (
	"strings"
	"testing"
)

// The replay confirmation page must show which session is selected on first
// paint (a real class, not a bare attribute) and expose it to assistive tech;
// the result area announces the outcome.
func TestReplayPageMarksTheSelectedSessionAccessibly(t *testing.T) {
	for _, session := range []string{"current", "flow"} {
		page := replayPageHTML(7, "GET", "https://example.com/a", 0, session)
		other := map[string]string{"current": "flow", "flow": "current"}[session]
		want := `data-s="` + session + `" class="active" aria-pressed="true"`
		if !strings.Contains(page, want) {
			t.Fatalf("session %q: selected button missing %q", session, want)
		}
		if !strings.Contains(page, `data-s="`+other+`" aria-pressed="false"`) {
			t.Fatalf("session %q: unselected button must be aria-pressed=false", session)
		}
		if strings.Contains(page, `" active>`) {
			t.Fatal("selected state rendered as a bare attribute, so .seg button.active never matches")
		}
		for _, s := range []string{`id="res" role="status" aria-live="polite"`, `c.setAttribute('aria-pressed',c===b?'true':'false')`} {
			if !strings.Contains(page, s) {
				t.Fatalf("replay page missing %q", s)
			}
		}
	}
}
