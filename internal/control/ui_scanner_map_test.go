package control

import (
	"strings"
	"testing"
)

// The windowed Map table (400+ endpoints) used to draw its header and its rows
// as two separate auto-layout tables, so the columns did not line up, and it
// assumed a 28px row while the stylesheet lays rows out at ~41px (44px on a
// coarse pointer): the window drifted off the scroll position and the last rows
// could not be reached. Arrow keys also wrapped inside the mounted window.
func TestMapVirtualTableIsOneMeasuredTableWithWholeTableKeys(t *testing.T) {
	js := readUIAsset(t, "js/map.js")
	requireUIContains(t, js,
		"function renderMapVirtualTable(",
		`<table class="map-tbl map-virt-body">${head}<tbody>`,
		"class=\"map-virt-pad\"",
		"computeVirtualWindow({ total: vt.sorted.length",
		"vt.rowH = h",
		"stepIndex({ current: cur, count: vt.sorted.length",
		"revealScrollTop({ rowTop: inset + next * vt.rowH",
	)
	for _, gone := range []string{"map-virt-head", "map-virt-spacer", "translateY(${start * MAP_ROW_H}px)"} {
		if strings.Contains(js, gone) {
			t.Fatalf("map.js still uses the split-table virtualizer (%q)", gone)
		}
	}
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css,
		"#mapTable .map-virt-scroll{overflow-anchor:none}",
		".map-virt-scroll>table{table-layout:fixed;min-width:640px}",
		".map-virt-pad td{padding:0;border:0;line-height:0;font-size:0}",
	)
}

// The Map's "+N identical" badge is a button inside a clickable row, so it
// needs its own 24px target (it was 47x18).
func TestMapClusterBadgeHasA24pxTarget(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "app.css"),
		".map-cluster-badge{display:inline-flex;align-items:center;gap:2px;min-height:24px;min-width:24px}")
}

// The scanner shows real progress from scan.progress events and can stop a scan
// through POST /api/scanner/cancel (a stopped scan saves nothing and is not
// reported as a failure). Results can be narrowed to one host, so traffic from
// hosts outside the engagement cannot bury the target's issues.
func TestUIScannerProgressStopAndHostFilter(t *testing.T) {
	region := uiRegion(t, "scanner")
	requireUIContains(t, region,
		`<button type="button" class="btn xs" id="scanStop">Stop scan</button>`,
		`<div class="scan-host-filter" id="scanHostRow" hidden><label for="scanHostFilter">Host</label><select id="scanHostFilter" class="btn btn-field">`,
	)
	js := readUIAsset(t, "js/scanner.js")
	requireUIContains(t, js,
		"registerSseHandler('scan.progress',onScanProgress)",
		"api('/api/scanner/cancel',{method:'POST'})",
		"if(e&&e.status===409&&scanStopPending){",
		"Scan stopped · nothing was saved; the previous results are unchanged",
		"const scoped=issuesForHost(scanState.issues,scanUI.host);",
		"stateEl.textContent=coverage||",
		"if(!fresh)loadIssues();",
		"if(row.hidden&&stopFocused)$('#scanRun')?.focus",
	)
}

// Clearing results renamed the button "Clear" (the markup says "Clear results"),
// the clear's own scanner.update reload wiped its confirmation, and a scope edit
// left the target list and Run gate stale until the panel was reopened.
func TestUIScannerClearAndScopeRefresh(t *testing.T) {
	js := readUIAsset(t, "js/scanner.js")
	requireUIContains(t, js,
		"if(button)button.textContent='Clear results';",
		"if(!cleared)loadIssues();",
		"registerSseHandler('scope.update',()=>{if(document.querySelector('#panel-scanner.active'))loadScanTargets();});",
	)
	if strings.Contains(js, "scanClearPending=false;if(button)button.textContent='Clear';") {
		t.Fatal("the Clear results button must keep its label after clearing")
	}
}
