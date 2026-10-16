package control

import (
	"regexp"
	"strings"
	"testing"
)

// interceptCSSRule returns the declaration block of the first standalone rule
// whose selector is exactly sel, so a layout contract cannot be satisfied by an
// unrelated rule.
func interceptCSSRule(t *testing.T, css, sel string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("stylesheet has no standalone %s rule", sel)
	}
	return m[1]
}

// TestInterceptActionBarStaysPinnedWhileMessageScrolls guards the held-message
// layout: the Pretty/Decoded <pre> views must scroll inside their own box. When
// they overflowed visibly, the whole #icptDetail scrolled and carried the
// Forward/Drop bar out of view.
func TestInterceptActionBarStaysPinnedWhileMessageScrolls(t *testing.T) {
	app := readUIAsset(t, "app.css")
	raw := interceptCSSRule(t, app, ".icpt-raw")
	for _, want := range []string{"flex:1 1 0", "min-height:0", "overflow:auto"} {
		if !strings.Contains(raw, want) {
			t.Errorf(".icpt-raw must keep %q so the editor/pretty/decoded views scroll internally", want)
		}
	}
	main := interceptCSSRule(t, app, ".icpt-main")
	for _, want := range []string{"display:flex", "flex-direction:column", "min-width:0"} {
		if !strings.Contains(main, want) {
			t.Errorf(".icpt-main must keep %q", want)
		}
	}
	scan := readUIAsset(t, "panel-scan.css")
	bar := interceptCSSRule(t, scan, ".icpt-actionbar")
	for _, want := range []string{"flex:none", "position:sticky", "bottom:0"} {
		if !strings.Contains(bar, want) {
			t.Errorf(".icpt-actionbar must keep %q so it never shrinks or scrolls away", want)
		}
	}
	// The editable Pretty view is an overlay editor inside .icpt-raw: the wrapper
	// clips, the textarea is the one that scrolls.
	prettyWrap := interceptCSSRule(t, app, ".icpt-raw.rep-field")
	for _, want := range []string{"overflow:hidden", "padding:0"} {
		if !strings.Contains(prettyWrap, want) {
			t.Errorf(".icpt-raw.rep-field must keep %q so only the editor scrolls", want)
		}
	}
	// Every held-message body view shares the scrolling class.
	html := readUIAsset(t, "index.html")
	for _, id := range []string{"heldDecoded", "heldPretty", "heldRaw"} {
		re := regexp.MustCompile(`<(?:pre|div|textarea)[^>]*\bid="` + id + `"[^>]*>`)
		tag := re.FindString(html)
		if tag == "" || !strings.Contains(tag, `class="icpt-raw`) {
			t.Errorf("#%s must use .icpt-raw so it scrolls inside the editor pane", id)
		}
	}
}
