package control

import (
	"regexp"
	"strings"
	"testing"
)

// The multi-document workbench canvas (#103) replaces the singleton `.panel.active`
// model with one or two document panes; the rail is the only navigation surface
// (the duplicate open-document tab strip was removed in #116). These static
// contracts pin the shell markup, the per-pane split/close controls, the
// split layout and the wiring that keeps every entry point and the panel-gated
// SSE model correct with two panels visible. The pure tab/split transitions are
// exercised under node (canvas-model.test.mjs via TestUIPrimitivesPureLogicUnderNode).

func TestUICanvasShellMarkup(t *testing.T) {
	index := readUIAsset(t, "index.html")
	// The strip + panes live inside the single <main> workspace.
	main := index[strings.Index(index, `<main id="main"`):]
	main = main[:strings.Index(main, "</main>")]
	requireUIContains(t, main,
		`<div id="canvas" class="canvas" data-layout="single">`,
		`<div class="canvas-pane" data-pane="0">`,
		`<div class="canvas-pane" data-pane="1">`,
		`<div id="canvasPool" class="canvas-pool" hidden>`,
	)
	// The sash is a keyboard-operable separator.
	requireUIRegex(t, main, `<div id="canvasSash" class="canvas-sash" role="separator"[^>]*tabindex="0"[^>]*aria-label="Resize split panes"`)
	// The redundant open-document tab strip is gone (#116): the rail is the only
	// navigation surface, so no strip markup, ids or document-tab rules remain.
	for _, gone := range []string{"docStrip", "docTabs", "doc-strip", "doc-tab", "doctabs"} {
		if strings.Contains(main, gone) {
			t.Errorf("workspace markup still references the removed tab strip (%q)", gone)
		}
	}
	// Split + close controls live in a per-pane action cluster, not a strip.
	requireUIContains(t, main, `<div id="paneActions" class="pane-actions" role="group" aria-label="Pane controls">`,
		`id="paneCloseBtn"`, `aria-label="Close this document"`, `href="#i-close"`)
	// Split controls are labelled buttons drawing sprite icons (no glyphs).
	for _, want := range []string{
		`id="splitLrBtn"`, `aria-label="Split view left and right"`, `href="#i-columns"`,
		`id="splitTbBtn"`, `aria-label="Split view top and bottom"`, `href="#i-panel-bottom"`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("canvas split control markup missing %q", want)
		}
	}
	// The static panels stay valid tabpanels inside the canvas (canvas.js relocates
	// them into panes at runtime); the rail tabs they belong to are preserved.
	for _, panel := range []string{"proxy", "repeater", "intruder", "findings"} {
		if !strings.Contains(main, `data-panel="`+panel+`"`) {
			t.Errorf("panel %q is gone from the canvas", panel)
		}
	}
}

func TestUICanvasModelExportsPureTransitions(t *testing.T) {
	model := executableJS(readUIAsset(t, "js/canvas-model.js"))
	for _, want := range []string{
		"export function createCanvasState(", "export function openDoc(", "export function focusDoc(",
		"export function closeDoc(", "export function splitCanvas(", "export function unsplit(",
		"export function focusPane(", "export function reorderDoc(",
		"export function visiblePanels(", "export const focusedPanel", "export const isSplit",
		"export function serializeCanvas(", "export function deserializeCanvas(",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("canvas-model.js missing %q", want)
		}
	}
	// Pure: no DOM access, no core.js import (also checked by TestUIPureModulesStayDOMFree).
	for _, banned := range []string{"document.", "window.", ".querySelector", "from './core.js'"} {
		if strings.Contains(model, banned) {
			t.Errorf("canvas-model.js references %q; it must stay DOM-free", banned)
		}
	}
}

func TestUICanvasControllerContracts(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/canvas.js"))
	requireUIContains(t, js,
		"export function initCanvas(", "export function canvasOpenDoc(",
		"export const canvasFocusedPanel", "export const canvasVisiblePanels", "export const canvasIsVisible",
		"export function canvasSplit(", "export function canvasUnsplit(", "export function canvasCloseDoc(",
		// visible == mounted in a pane: canvas owns the `.panel.active` flag the SSE
		// gating reads, and marks the focused panel so shortcuts can find it.
		"el.classList.add('active')", "'is-focused'",
		// focus reflects onto the rail tabs so the dock/rail stay in sync.
		"#tabs .tab", "aria-selected",
		// the per-pane close control closes the focused document; no strip remains.
		"paneCloseBtn", "paneActions",
		// layout + sizing reuse the pure split geometry and persist per project.
		"clampSplitSize", "projectStorageKey('canvasLayout')",
	)
	for _, gone := range []string{"docTabs", "docStrip", "renderStrip", "wireStrip", "doc-tab"} {
		if strings.Contains(js, gone) {
			t.Errorf("canvas.js still references the removed tab strip (%q)", gone)
		}
	}
	// The split pairs two documents; parked panels move to the hidden pool.
	requireUIContains(t, js, "els.pool.appendChild", "placeInPane(els.pane1")
	// Phones never split.
	if !strings.Contains(js, "narrowMq") {
		t.Error("canvas.js must disable the split on phone widths")
	}
}

func TestUICanvasWiredIntoShell(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"import { initCanvas, canvasOpenDoc, canvasFocusedPanel } from './canvas.js'",
		// Rail clicks and the shell API open/focus a document in the canvas.
		"function activateTab(t){",
		"canvasOpenDoc(t.dataset.tab)",
		// The canvas is built during project-scoped hydration, before restoreTab.
		"initCanvas({initialPanel:resolveSavedTab()",
		"onFocusedChange:onCanvasFocusChange",
		// Shortcuts target the FOCUSED pane's panel.
		"document.querySelector('.panel.is-focused')",
		// Live/gated work keys off panel visibility, not the rail's active tab, so a
		// panel sharing a split keeps rendering.
		"function isPanelVisible(name)",
		"if(!isPanelVisible('map'))setNavDot('mapBadge',true)",
	)
}

func TestUICanvasSplitCSSShowsTwoPanes(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "canvas.css"))
	// A single layout hides the second pane and the sash; a split reveals both.
	requireUIRegex(t, css, `#canvas\[data-layout="single"\] \.canvas-pane\[data-pane="1"\][^{]*\{display:none\}`)
	requireUIRegex(t, css, `#canvas\[data-layout\^="split"\] \.canvas-pane\[data-pane="0"\]\{flex:0 0 var\(--canvas-split\)\}`)
	requireUIContains(t, css,
		`#canvas[data-layout^="split"] .canvas-pane[data-pane="1"]{flex:1 1 0}`,
		`.canvas-pane{position:relative`,
		`.canvas-sash{`,
	)
	// The focused pane is distinguished while split, and the phone model collapses
	// a split to a single pane.
	requireUIContains(t, css, `.canvas-pane[data-focused="true"]`)
	if !regexp.MustCompile(`@media \(max-width:720px\)\{[^@]*\.canvas-pane\[data-pane="1"\]`).MatchString(css) {
		t.Error("canvas.css must collapse the split to one pane on phones")
	}
	// Only transform/opacity may animate: this sheet declares neither transition
	// nor @keyframes (the entrance is a JS one-shot).
	if strings.Contains(css, "transition:") || strings.Contains(css, "@keyframes") {
		t.Error("canvas.css must not animate; the panel entrance is a JS one-shot in canvas.js")
	}
}

func TestUICanvasStripRemovedFromCSS(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "canvas.css"))
	for _, gone := range []string{"#docStrip", ".doc-tab", ".doc-strip", "--doctabs-h", ".doc-split-btn"} {
		if strings.Contains(css, gone) {
			t.Errorf("canvas.css still carries removed strip rule %q", gone)
		}
	}
	// The panes fill the whole workspace; the action cluster floats over the focused pane.
	requireUIRegex(t, css, `#canvas\{[^}]*top:0`)
	requireUIContains(t, css, ".pane-actions{", ".pane-btn")
}
