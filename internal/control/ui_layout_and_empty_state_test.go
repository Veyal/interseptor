package control

import (
	"strings"
	"testing"
)

// The Flight Bar carried three unlabelled icon buttons (split left/right, split
// top/bottom, close document). They are driven from one labelled Layout menu
// now: the buttons stay in the DOM (ids, handlers, aria-pressed) but only the
// trigger is painted, so the header shows one control instead of three cryptic
// icons.
func TestUIPaneControlsLiveInOneLayoutMenu(t *testing.T) {
	canvas := readUIAsset(t, "js/canvas.js")
	css := readUIAsset(t, "canvas.css")

	for _, want := range []string{
		"function wirePaneMenu()",
		"btn.id = 'paneMenuBtn'",
		"btn.setAttribute('aria-haspopup', 'menu')",
		"openCtxMenu(r.left, r.bottom,",
		"label: 'Split left / right'",
		"label: 'Split top / bottom'",
		"label: 'Close this document'",
		// The rows press the original buttons so their state stays the one truth.
		"act: () => els.splitLr && els.splitLr.click()",
		"act: () => els.splitTb && els.splitTb.click()",
		"wirePaneMenu();",
	} {
		if !strings.Contains(canvas, want) {
			t.Errorf("canvas.js is missing the Layout-menu contract %q", want)
		}
	}
	if !strings.Contains(css, "#paneActions.pane-menu .pane-btn:not(#paneMenuBtn){display:none}") {
		t.Error("canvas.css must hide the three icon buttons behind the Layout trigger")
	}
	if strings.Contains(canvas, "btn.setAttribute('aria-expanded', 'true')") {
		t.Error("the trigger must let openCtxMenu own aria-expanded")
	}
}

// An empty collection used to show three empty states at once (tree card,
// "Select a request", "No response yet"). The tree card is the one that can act,
// the editor answers only when there is something to pick, and the response pane
// waits for an open request instead of asking a third time.
func TestUICollectionsShowOneEmptyState(t *testing.T) {
	editor := readUIAsset(t, "js/collections-editor.js")
	response := readUIAsset(t, "js/collections-response.js")
	css := readUIAsset(t, "css/collections.css")

	for _, want := range []string{
		"if (!(S.collection && (S.items || []).length)) { host.textContent = ''; X.renderResponse(); return; }",
		"title: 'Select a request'",
	} {
		if !strings.Contains(editor, want) {
			t.Errorf("collections-editor.js is missing the single-empty-state contract %q", want)
		}
	}
	if strings.Contains(response, "title: 'No response yet', hint: 'Open a request and press Send.'") {
		t.Error("the response pane must stay blank until a request is open")
	}
	for _, want := range []string{
		".coll-root:has(.coll-state .state-empty)>.coll-detail",
		".coll-root .coll-main:has(> .coll-editor > .state-empty)>.coll-response{display:none}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("collections.css must collapse the panes that have nothing to show, missing %q", want)
		}
	}
}
