package control

import (
	"strings"
	"testing"
)

// Regression: Proxy → Map → Proxy returned History to the top. Moving a panel
// into the hidden pool and back resets every scroller inside it, so canvas.js
// remembers offsets on the way out and restores them on the way back. Both
// steps are order-sensitive: offsets must be read before 'active' is removed
// (a display:none scroller reports 0) and written after it is added back (a
// display:none scroller ignores writes).
func TestUICanvasKeepsPanelScrollAcrossDocumentSwitches(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/canvas.js"))
	out := strings.Index(js, "rememberScroll(el);")
	hide := strings.Index(js, "el.classList.remove('active', 'is-focused');")
	if out < 0 || hide < 0 || out > hide {
		t.Fatal("canvas.js must remember panel scroll offsets before removing 'active'")
	}
	show := strings.Index(js, "el.classList.add('active');")
	back := strings.Index(js, "if (moved) restoreScroll(el);")
	if show < 0 || back < 0 || back < show {
		t.Fatal("canvas.js must restore panel scroll offsets after adding 'active'")
	}
}
