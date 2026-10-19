package control

import (
	"strings"
	"testing"
)

// Regression: at 900x610 with Match & Replace open, the desktop Intercept
// editor kept its 240px minimum, the detail pane scrolled, and the static
// action bar (Forward, Drop) slid out of view under the rules table. The
// editor may shrink, the bar stays pinned, and the rules drawer is capped.
func TestUIDesktopInterceptActionBarStaysVisibleOnShortWindows(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-intercept.css")
	for _, want := range []string{
		"html.desktop-app #panel-intercept .icpt-main .icpt-raw{min-height:96px}",
		"html.desktop-app #panel-intercept .icpt-actionbar{position:sticky;bottom:0;",
		"html.desktop-app #panel-intercept .icpt-mr-body{max-height:min(240px,28vh);",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-intercept.css lacks %q", want)
		}
	}
}

// Regression: with many Repeater tabs the active tab was clipped at the right
// edge and "+" was scrolled out of sight. The add button sticks to the end of
// the strip and a render scrolls the active tab into view.
func TestUIRepeaterTabStripKeepsActiveTabAndAddVisible(t *testing.T) {
	app := readUIAsset(t, "app.css")
	if !strings.Contains(app, ".rep-tab-add{position:sticky;right:0;z-index:1;") || !strings.Contains(app, "scroll-padding-inline-end:") {
		t.Fatal("app.css: the tab strip's + must stick to the right edge, with scroll padding for the active tab")
	}
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, "revealActiveTab(bar)") {
		t.Fatal("core.js: the tab manager must scroll the active tab into view after a render")
	}
	desk := readUIAsset(t, "css/desktop/components.css")
	if !strings.Contains(desk, "html.desktop-app .rep-tabs .rep-tab-add{background-color:var(--surface-0)}") {
		t.Fatal("desktop: the sticky + needs an opaque background over scrolled tabs")
	}
}

// Regression: the Hex views fetched /raw as text, so every byte >= 0x80 of a
// binary body became U+FFFD and dumped as "ef bf bd". They fetch bytes.
func TestUIHexViewsFetchRawBytes(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, "export async function apiBytes(") || !strings.Contains(core, "arrayBuffer()") {
		t.Fatal("core.js needs apiBytes, which reads the body as bytes")
	}
	for _, f := range []string{"js/tools.js", "js/proxy.js"} {
		js := readUIAsset(t, f)
		// The dump goes through mountHexDump (which fits the line width to the
		// pane) and must be fed the bytes from apiBytes, not decoded text.
		i := strings.Index(js, "mountHexDump(")
		if i < 0 || !strings.Contains(js[max(0, i-400):i], "apiBytes(") || !strings.Contains(js[i:i+60], ",bytes)") {
			t.Errorf("%s: the Hex view must dump bytes from apiBytes, not decoded text", f)
		}
	}
}

// Regression: Attach as evidence and Save as proof each stacked another
// identical "Findings editing is off" toast. The gate replaces its own notice.
func TestUIFindingsGateToastDoesNotStack(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	i := strings.Index(core, "export function requireFindingsEditing(){")
	if i < 0 {
		t.Fatal("requireFindingsEditing missing")
	}
	body := core[i : i+strings.Index(core[i:], "\n}\n")]
	if !strings.Contains(body, `querySelectorAll('#toast [data-toast-key="findings-off"]')`) || !strings.Contains(body, "t.dataset.toastKey='findings-off'") {
		t.Fatal("requireFindingsEditing must replace an earlier findings-off toast instead of stacking a second one")
	}
}

// Regression: in a narrow Repeater response pane (900px window, History open)
// the status line was cut to "500 Internal Server Error · 1 m…", and the Diff
// header's margin-right:auto title pushed "Ignore dates, cookies, tokens"
// against the pane's right edge.
func TestUIRepeaterNarrowStatusAndDiffHeader(t *testing.T) {
	desk := readUIAsset(t, "css/desktop/panel-repeater.css")
	if !strings.Contains(desk, "@container dsk-rep-res (max-width:420px){") || !strings.Contains(desk, "html.desktop-app #panel-repeater .dsk-actionrow #repStatus{flex:1 0 100%;white-space:normal") {
		t.Error("desktop: a narrow response pane must give the status line its own wrapping row")
	}
	js := readUIAsset(t, "js/desktop-panel-repeater.js")
	if !strings.Contains(js, "status.title = status.textContent") {
		t.Error("desktop: the status line must carry its full text as a tooltip")
	}
	tools := readUIAsset(t, "panel-tools.css")
	if !strings.Contains(tools, ".rep-diff-head strong{flex:1 0 100%;") || !strings.Contains(tools, ".rep-diff-ignore{display:inline-flex;align-items:center;gap:6px}") {
		t.Error("the Diff header title takes its own row and the ignore checkbox label keeps a gap")
	}
	if !strings.Contains(readUIAsset(t, "js/repeater.js"), "mk('label', 'hint rep-diff-ignore')") {
		t.Error("repeater.js: the ignore label needs the rep-diff-ignore class")
	}
}
