package control

import (
	"strings"
	"testing"
)

// Issue #102 — the Decoder is demoted from a centered blocking modal to a
// slide-up bottom "Convert" drawer, reachable globally with Cmd/Ctrl+J, while
// staying a focus-trapped MODAL_IDS entry (Esc, scrim, focus restore).
func TestUIDecoderIsABottomConvertDrawer(t *testing.T) {
	css := readUIAsset(t, "surfaces.css")
	requireUIContains(t, css, "#decModal{align-items:flex-end", "@keyframes dec-slide-up")
	// Still a registered modal so the drawer keeps its focus trap.
	if !strings.Contains(readUIAsset(t, "js/core.js"), "'decModal'") {
		t.Error("decModal must stay in MODAL_IDS so the bottom drawer keeps its focus trap")
	}
	app := executableJS(readUIAsset(t, "js/app.js"))
	if !strings.Contains(app, "isModShortcut(e,'j')){e.preventDefault();openDecoder()") {
		t.Error("Cmd/Ctrl+J must open the Decoder drawer globally")
	}
	// The slide animation is transform-only (compositor-safe).
	kf := css[strings.Index(css, "@keyframes dec-slide-up"):]
	kf = kf[:strings.Index(kf, "}")+1]
	for _, bad := range []string{"width", "height", "margin", "left", "top", "background"} {
		if strings.Contains(kf, bad+":") {
			t.Errorf("dec-slide-up keyframe animates %q; only transform/opacity are allowed", bad)
		}
	}
}
