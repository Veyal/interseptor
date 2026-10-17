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
