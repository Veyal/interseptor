package control

import (
	"strings"
	"testing"
)

// Notes has the standard pane header in both layouts (title, save status, then
// actions), like every other panel; it never floats as a corner overlay.
func TestUIDesktopNotesHasTheStandardPaneHeader(t *testing.T) {
	css := assertDesktopPanelSheetFNS(t, "css/desktop/panel-notes.css")
	for _, want := range []string{
		// one 40px header row with the pane title
		`#panel-notes>.toolbar{flex-wrap:nowrap;gap:8px;height:var(--toolbar-h)`,
		`#panel-notes>.toolbar::before{content:"Notes"`,
		`font-size:var(--text-title);line-height:var(--lh-title);font-weight:600`,
		// document reading: 14/21 in the UI font, near 72ch
		`.notes-edit{padding:24px max(24px,calc(50% - 38ch))`,
		`font-family:var(--ui);font-size:var(--text-body);line-height:var(--lh-body)`,
		// renderMD's <br> padding no longer makes 50px section gaps
		`#notesPreview .acc-body>br{display:none}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-notes.css lost %q", want)
		}
	}
	if strings.Contains(css, "toolbar{position:absolute") || strings.Contains(css, ">.toolbar .btn:not(#notesSaveRetry){opacity:0") {
		t.Error("the Notes header must be a real row with visible actions, not a hover overlay")
	}
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"notesSeg", "notesStatus", "notesSaveRetry", "notesPromote", "notesEdit", "notesPreview"} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
}
