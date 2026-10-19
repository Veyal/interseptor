package control

import (
	"strings"
	"testing"
)

// Regression: scheduleIntr cleared and re-armed its 400 ms timer on every
// intruder.update, so a fast attack never polled until it ended.
func TestUIIntruderPollTimerIsNotRearmed(t *testing.T) {
	js := readUIAsset(t, "js/tools.js")
	i := strings.Index(js, "export function scheduleIntr(){")
	if i < 0 {
		t.Fatal("scheduleIntr missing")
	}
	body := js[i : i+400]
	if strings.Contains(body, "clearTimeout") {
		t.Fatal("scheduleIntr must not clear an armed timer")
	}
	if !strings.Contains(body, "intrTimerArmed") {
		t.Fatal("scheduleIntr must return early while the poll timer is armed")
	}
}

func TestUIIntruderOptionAndPresetContracts(t *testing.T) {
	html := readUIAsset(t, "index.html")
	if !strings.Contains(html, `id="intrDelay" type="number" min="0" max="60000"`) {
		t.Fatal("the delay input needs max=60000")
	}
	if strings.Contains(readUIAsset(t, "js/tools.js"), `<option value="">presets…</option>`) {
		t.Fatal("preset placeholder must read 'Saved presets…' like the markup")
	}
}

// Regression: the Map graph had no context menu, the pinch gesture of
// Safari/WKWebView was ignored, and the status note had no tooltip.
func TestUIMapGraphMenuAndPinch(t *testing.T) {
	js := readUIAsset(t, "js/map.js")
	for _, want := range []string{"'#mapGraphG'", ".g-node[data-flow]", "'gesturestart'", "'gesturechange'", "gestureZoom("} {
		if !strings.Contains(js, want) {
			t.Fatalf("map.js lacks %s", want)
		}
	}
	if !strings.Contains(readUIAsset(t, "js/scanner.js"), "noteEl.title=t") {
		t.Fatal("scanner status note must mirror its text into a tooltip")
	}
}

// Regression (round 2): on the web UI at 900px the third payload list ran
// under Results, rows were covered by text chips, and the desktop preset
// picker stayed "Saved p…". The layout contracts live in CSS, so pin them.
func TestUIIntruderRound2LayoutContracts(t *testing.T) {
	web := readUIAsset(t, "panel-tools.css")
	for _, want := range []string{
		"html:not(.desktop-app) .intr-work{display:grid;",
		"html:not(.desktop-app) .intr-work>.intr-payload-pane{grid-column:2;grid-row:2;",
		".intr-row .intr-chip-text{display:none}",
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("panel-tools.css lacks %q", want)
		}
	}
	desk := readUIAsset(t, "css/desktop/panel-intruder.css")
	for _, want := range []string{
		"min-content min-content minmax(96px,.7fr) minmax(196px,1.3fr) min-content",
		".intr-pl textarea{min-height:96px}",
		"@media (max-width:1279px)",
	} {
		if !strings.Contains(desk, want) {
			t.Fatalf("desktop panel-intruder.css lacks %q", want)
		}
	}
	if !strings.Contains(readUIAsset(t, "js/desktop-panel-intruder.js"), "observe(runtime, () => {") {
		t.Fatal("the desktop preset label must be renamed by an observer on the whole options row")
	}
}

// Regression (round 3): Intruder payload-list, marker and hint behaviour.
func TestUIIntruderRound3Contracts(t *testing.T) {
	js := readUIAsset(t, "js/tools.js")
	for _, want := range []string{
		"intr-file-clear",                          // large lists stay clearable
		"select the text to mark first",            // § Mark with no selection inserts nothing
		"if(a===b){toast(",                         //
		"/§[^§]+§/g",                               // empty markers are never counted
		"hint.textContent='fix the Numbers range'", // no payload count for an invalid range
		"read-only preview",                        // the reason a long list cannot be typed in
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("tools.js lacks %q", want)
		}
	}
	css := readUIAsset(t, "panel-tools.css")
	if strings.Contains(css, ".intr-results>.pane-head{flex-wrap:nowrap;overflow-x:auto") {
		t.Fatal("web Results header must wrap, not scroll sideways invisibly")
	}
}

// Regression (round 3): Scanner/Map layout and state contracts.
func TestUIScannerMapRound3Contracts(t *testing.T) {
	scan := readUIAsset(t, "css/desktop/panel-scanner.css")
	for _, want := range []string{
		"@media (max-width:1100px)",
		"#scanRescanState{order:99;flex:1 0 100%",
		"#scanPassiveBar .ui-select{flex:0 1 190px;min-width:176px",
	} {
		if !strings.Contains(scan, want) {
			t.Fatalf("panel-scanner.css lacks %q", want)
		}
	}
	mapCSS := readUIAsset(t, "css/desktop/panel-map.css")
	if strings.Contains(mapCSS, "#mapCount{flex:0 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis") {
		t.Fatal("desktop #mapCount must wrap, not ellipsise the 'before collapsing' note")
	}
	if !strings.Contains(readUIAsset(t, "app.css"), ".map-virt-body td.map-tbl-act{text-overflow:clip}") {
		t.Fatal("the Map table action cell must not paint an ellipsis for its hidden button")
	}
	js := readUIAsset(t, "js/map.js")
	for _, want := range []string{
		"_reopenFolders",     // cluster badge keeps opened sub-folders
		`data-fkey=`,         //
		"graphLabelChars(n)", // Fit bounds include label extents
		"getBBox",            //
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("map.js lacks %q", want)
		}
	}
}
