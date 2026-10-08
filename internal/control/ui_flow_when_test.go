package control

import (
	"regexp"
	"strings"
	"testing"
)

// The History Time column shows a date-aware timestamp (flow-when.js). These
// static checks pin the wiring that the node tests (ui/_js-tests/flow-when.test.mjs)
// cannot see: the module stays DOM-free, the column default is wide enough for
// the dated form, the cell markup carries the full text for assistive tech, a
// legacy saved 60px width upgrades, and the midnight refresh cleans up.

func TestUIFlowWhenModuleIsDOMFree(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/flow-when.js"))
	if regexp.MustCompile(`(?m)^import `).MatchString(src) {
		t.Error("flow-when.js must not import anything (it runs under node)")
	}
	for _, banned := range []string{"document.", "window.", "toLocale", "Intl."} {
		if strings.Contains(src, banned) {
			t.Errorf("flow-when.js uses %q; it must be DOM-free and locale-independent", banned)
		}
	}
}

func TestUIFlowTimeColumnDefaultsAndCell(t *testing.T) {
	js := readUIAsset(t, "js/proxy.js")
	if !strings.Contains(js, "const TIME_COL_W=124") {
		t.Error("time column default width should be 124px (dated form must not clip)")
	}
	if !strings.Contains(js, "{key:'time',label:'Time',sort:'time',w:TIME_COL_W+'px',align:'right'}") {
		t.Error("FLOW_COLUMNS time entry must use TIME_COL_W")
	}
	if !strings.Contains(js, "c.key==='time'&&v===LEGACY_TIME_COL_W") || !strings.Contains(js, "LEGACY_TIME_COL_W=60") {
		t.Error("a saved 60px time width (the old default) must auto-upgrade to the new default")
	}
	if regexp.MustCompile(`\bfmtTime\b`).MatchString(js) {
		t.Error("proxy.js must format flow timestamps with formatFlowWhen, not fmtTime")
	}
	start := strings.Index(js, "function flowTimeCell(f){")
	if start < 0 {
		t.Fatal("flowTimeCell not found")
	}
	cell := js[start : start+strings.Index(js[start:], "function flowRowHTML")]
	for _, want := range []string{"formatFlowWhen(f.ts)", `class="when-date"`, `class="when-time"`, `title="${escAttr(w.title)}"`, `<span class="u-sr">${esc(w.title)}</span>`} {
		if !strings.Contains(cell, want) {
			t.Errorf("flowTimeCell missing %s", want)
		}
	}
	css := readUIAsset(t, "app.css")
	if !regexp.MustCompile(`\.when-time\{[^}]*tabular-nums`).MatchString(css) || !strings.Contains(css, ".when-date{color:var(--fg3)") {
		t.Error("app.css must style .when-time (tabular-nums) and a dim .when-date")
	}
	if !strings.Contains(css, "52px 64px 124px var(--row-more-w))}") {
		t.Error("the pre-JS fallback grid template must use the 124px time track")
	}
}

func TestUIFlowTimeMidnightRefreshCleansUp(t *testing.T) {
	js := readUIAsset(t, "js/proxy.js")
	for _, want := range []string{"msUntilNextMidnight()", "clearTimeout(midnightTimer)", "refreshVisibleRows()", "'visibilitychange'", "'focus'", "'pagehide'"} {
		if !strings.Contains(js, want) {
			t.Errorf("midnight refresh missing %s", want)
		}
	}
}

// Phone cards give the status and time cells one shared fixed-width track, so a
// dated time ("2025-12-31 23:59") does not push the status column around row by row.
func TestUIFlowTimePhoneCardUsesFixedTimeTrack(t *testing.T) {
	css := readUIAsset(t, "panel-proxy.css")
	if !strings.Contains(css, "grid-template-columns:auto minmax(0,1fr) auto 7.5rem var(--hit-min)") {
		t.Error("phone row grid must reserve a fixed 7.5rem track for the status/time column")
	}
	if !strings.Contains(css, ".tr-st{grid-area:st;text-align:right}") {
		t.Error("phone status cell should right-align over the time")
	}
}
