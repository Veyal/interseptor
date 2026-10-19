package control

import (
	"strings"
	"testing"
)

// Regression: the Proxy inspector's request header carries the view tabs plus
// Repeater, Intruder, Copy as PNG, Copy as and More. At ordinary desktop widths
// (1280-1920px) it needs more room than half the inspector, and with a fixed
// 38px, non-wrapping row the later buttons were drawn underneath the response
// pane where no click could reach them. The header must wrap at every width,
// as the Repeater and Intruder headers already do, not only on phones.
func TestUIProxyInspectorHeaderWrapsAtEveryWidth(t *testing.T) {
	css := readUIAsset(t, "app.css")
	rule := "#inspect .pane-head{height:auto;min-height:38px;flex-wrap:wrap;row-gap:4px;padding-block:4px}"
	i := strings.Index(css, rule)
	if i < 0 {
		t.Fatalf("app.css lacks the wrapping inspector header rule %q", rule)
	}
	// It must sit at the top level, not inside an @media block.
	depth := 0
	for _, c := range css[:i] {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if depth != 0 {
		t.Fatalf("the inspector header wrap rule is nested %d levels deep; it must apply at every width", depth)
	}
}
