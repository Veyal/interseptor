package control

import (
	"strings"
	"testing"
)

// Regression: a tab strip rendered while its panel was display:none could not
// measure itself, so the active tab stayed off-screen once the panel opened.
func TestUIPanelShownRevealsActiveTab(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	start := strings.Index(app, "function loadPanelData(name){")
	if start < 0 {
		t.Fatal("loadPanelData not found")
	}
	requireUIContains(t, app[start:start+400], `.panel[data-panel="'+name+'"] .rep-tabs').forEach(revealActiveTab)`)
}

// Regression: edits made in the Decoded view were lost when switching views, and
// Raw kept the pre-edit wire body even after a Send that transmitted the edit.
func TestUIRepeaterDecodedEditReEncodesToWire(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"async function repEncodeWire(t)",
		"'/api/codecs/encode'",
		"t.applyOnSend&&t.codecId&&t.decodedBase!==t.decodedPlain",
		"t.body=wire;t.rawBody=wire;t.decodedBase=t.decodedPlain;",
		"t.decodedBase=t.decodedPlain;\n    if(repCur()!==t)return true;",
	)
	if strings.Count(tools, "const wire=await repEncodeWire(t);if(wire!==null){t.body=wire;") != 1 {
		t.Error("a successful Decoded send must refresh the wire body once")
	}
}

// Regression: at 900x610 the Hex view clipped its ASCII column because even the
// 8-byte layout did not fit; a 4-byte layout is the narrowest fallback.
func TestUIHexDumpHasNarrowestLayout(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "js/hexdump.js"), "export const HEX_LINE_4 = 4;", "avail >= HEX_CHARS_8 * charW ? HEX_LINE_8 : HEX_LINE_4")
}

// Regression: Esc left the Repeater History rail open and a right click on a row
// showed only the generic workspace menu.
func TestUIRepeaterHistoryEscAndRowMenu(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"historyOpen:repHistoryVisible(),",
		"else if(action==='history'){",
		"$('#repHistory').addEventListener('contextmenu'",
		"{label:'Load this send'",
	)
	requireUIContains(t, readUIAsset(t, "js/repeater-tab-model.js"), "if (historyOpen) return 'history';")
}

// Regression: a stack of toasts scrolled inside a click-through container, which
// hid the newest one below the fold.
func TestUIToastStackKeepsNewestVisible(t *testing.T) {
	css := readUIAsset(t, "surfaces.css")
	start := strings.Index(css, "#toast {")
	if start < 0 {
		t.Fatal("#toast rule not found")
	}
	rule := css[start : start+900]
	requireUIContains(t, rule, "overflow: hidden;", "justify-content: flex-end;")
	if strings.Contains(rule[:strings.Index(rule, "}")], "overflow-y: auto") {
		t.Error("#toast must not scroll internally")
	}
}

// Regression: the desktop skin's palette margin overrode the phone sheet.
func TestUIDesktopCommandPaletteNarrowSheetWins(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "css/desktop/components.css"), "@media (min-width:721px){html.desktop-app .cmdk-shell{margin-top:14vh;")
}

// Regression: first-run checklist labels inherited the empty state's centering.
func TestUIChecklistStepsAreLeftAligned(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "settings.css"), ".checklist-step{text-align:left;")
}
