package control

import (
	"regexp"
	"strings"
	"testing"
)

// Regression: the desktop skin set overscroll-behavior:none on every element.
// Each truncating History cell is an overflow:hidden scroll container, so a
// wheel over a row stopped at the cell instead of scrolling #rows, and History
// never reached "load older flows". Only the page itself may opt out.
func TestUIDesktopOverscrollDoesNotBlockWheelChaining(t *testing.T) {
	css := readUIAsset(t, "desktop.css")
	if regexp.MustCompile(`html\.desktop-app \*[^{]*\{[^}]*overscroll-behavior`).MatchString(css) {
		t.Fatal("desktop.css must not set overscroll-behavior on html.desktop-app *: it stops wheel scrolling over list cells")
	}
	if !strings.Contains(css, "html.desktop-app,html.desktop-app body{overscroll-behavior:none}") {
		t.Fatal("desktop.css should still stop rubber-band overscroll on the page itself")
	}
	if !strings.Contains(css, "html.desktop-app #rows,html.desktop-app .trow,html.desktop-app .trow>div{overscroll-behavior:auto}") {
		t.Fatal("desktop.css must explicitly keep overscroll-behavior:auto on flow list rows and cells")
	}
}

// Regression: overlay editors (a transparent textarea over a highlighted pre)
// went blank while focused because the desktop focus state gave the textarea an
// opaque background. They must stay transparent on focus.
func TestUIDesktopOverlayEditorsStayTransparentOnFocus(t *testing.T) {
	css := readUIAsset(t, "css/desktop/states.css")
	rule := "html.desktop-app:not([data-theme=\"hc\"]):not(#dsk-x) :is(.rep-field>textarea.rep-edit,.se-ta,#dsk-x):focus-visible{background-color:transparent}"
	i := strings.Index(css, rule)
	if i < 0 {
		t.Fatalf("states.css lacks the overlay-editor focus exemption %q", rule)
	}
	for _, earlier := range []string{":is(.rep-edit,.notes-edit,.icpt-raw", "textarea,select,[contenteditable=\"true\"]"} {
		if j := strings.Index(css, earlier); j < 0 || j > i {
			t.Fatalf("the exemption must come after the focus rule containing %q so it wins the cascade", earlier)
		}
	}
}

// Regression: the desktop shell reset #main's margin, which removed the web
// rule that makes room for the docked flow drawer, so the drawer covered the
// right end of the active panel (Activity's Copy/Clear buttons).
func TestUIDesktopMainMakesRoomForDockedFlowDrawer(t *testing.T) {
	css := readUIAsset(t, "css/desktop/shell.css")
	reset := strings.Index(css, "html.desktop-app #main{margin:0;")
	room := strings.Index(css, "html.desktop-app body.flow-drawer-docked #main{margin-right:var(--flow-drawer-w)}")
	if reset < 0 || room < 0 || room < reset {
		t.Fatal("shell.css must give #main a right margin for the docked flow drawer after resetting its margin")
	}
}

// Regression: the first preview block had a 6px top margin, so the Notes
// preview text sat lower than the editor text beside it.
func TestUIDesktopNotesPreviewStartsOnTheEditorLine(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-notes.css")
	if !strings.Contains(css, "html.desktop-app #notesPreview>:first-child{margin-top:0}") {
		t.Fatal("panel-notes.css must drop the first preview block's top margin")
	}
}

// Regression (Settings cosmetics): fixed pixel widths clipped the mono
// placeholders ("their full-access key (ick_…)", "label (optional)", the peer URL); the
// desktop h3 reset swallowed the gap above "Project vault"; and "Run setup
// wizard again" touched its hint.
func TestUISettingsShareFieldsFitTheirPlaceholders(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, want := range []string{
		"#peerKey{flex:none;width:calc(24ch + 28px);max-width:100%}",
		"#peerUrl{flex:1;min-width:min(100%,calc(34ch + 28px))}",
		"#vaultUrl{flex:1;min-width:min(100%,calc(37ch + 28px))}",
		"#allowLabel,#peerLabel,#vaultBackupLabel{flex:none;width:calc(17ch + 28px);max-width:100%}",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("app.css lacks %q", want)
		}
	}
	for _, stale := range []string{"#peerKey{max-width:200px}", "#vaultBackupLabel{max-width:140px}", "#keyScope,#keyExpiry,#peerLabel,"} {
		if strings.Contains(css, stale) {
			t.Fatalf("app.css still has the clipping width %q", stale)
		}
	}
	desk := readUIAsset(t, "css/desktop/panel-settings.css")
	if !strings.Contains(desk, "html.desktop-app .settings-body .section h3.micro-label{margin-top:16px}") {
		t.Fatal("desktop settings must keep a gap above sub-block labels such as Project vault")
	}
	html := readUIAsset(t, "index.html")
	if !strings.Contains(html, `<div class="row u-gap-3 u-mt-3"><button class="btn" id="runSetupBtn"`) {
		t.Fatal("the Run setup wizard row needs a gap between the button and its hint")
	}
}

// Regression (native shell, full screen): the Settings section list was a 22%
// split, so it grew to ~520px while the 720px cards hugged it and left the
// rest of the window empty. The list is capped and the column is centred.
func TestUIDesktopSettingsStaysBalancedOnWideWindows(t *testing.T) {
	js := readUIAsset(t, "js/settings.js")
	if !strings.Contains(js, "min:[200,360],maxList:320,default:22") {
		t.Fatal("settings split must cap the section list width")
	}
	css := readUIAsset(t, "css/desktop/panel-settings.css")
	if !strings.Contains(css, "html.desktop-app .settings-body>.set-sec:not([hidden]){max-width:720px;margin-inline:auto}") {
		t.Fatal("desktop settings column must be centred in the pane")
	}
}

// Regression: overlay editors select through a transparent textarea, so their
// ::selection paints only a tint over the highlighted text below. The 14% accent
// tint (--accentDim) was nearly invisible in dark Repeater. A 28% tint is visible and
// keeps accent-coloured tokens readable through it, in every theme.
func TestUIOverlayEditorSelectionIsVisible(t *testing.T) {
	const tint = "{background:color-mix(in srgb,var(--accent) 28%,transparent);color:transparent}"
	for _, c := range []struct{ file, sel string }{
		{"app.css", ".rep-field>textarea.rep-edit::selection"},
		{"css/collections.css", ".se-ta::selection"},
	} {
		if css := readUIAsset(t, c.file); !strings.Contains(css, c.sel+tint) {
			t.Errorf("%s: %s must use the visible translucent tint %q", c.file, c.sel, tint)
		}
	}
}

// Regression: desktop Collections hides the tree's empty state in favour of the
// editor's single panel-wide one, but in the stacked layout (a narrow window) the
// editor is not shown, so the panel was blank. The tree state may only yield to
// the editor when the editor is beside it.
func TestUIDesktopCollectionsEmptyStateSurvivesTheStackedLayout(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-collections.css")
	if strings.Contains(css, "html.desktop-app #panel-collections:has(.coll-editor .state-empty) .coll-state{display:none}") {
		t.Fatal("the tree empty state is hidden even when the editor is not on screen")
	}
	want := `html.desktop-app #panel-collections .coll-root:not([data-mode="stack"]):has(.coll-editor .state-empty) .coll-state{display:none}`
	if !strings.Contains(css, want) {
		t.Fatalf("panel-collections.css lacks %q", want)
	}
	if !strings.Contains(css, "html.desktop-app #panel-collections .coll-tree:empty+.coll-state:not(:empty){flex:1 1 auto;display:flex;flex-direction:column;justify-content:center") {
		t.Fatal("the tree empty state must be centred in the column, not pinned under an empty tree")
	}
}
