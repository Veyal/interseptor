package control

import (
	"strings"
	"testing"
)

// At 900x610 the three payload lists are ~122px wide: the large-list notice must
// wrap instead of clipping, the editors keep a usable height, and the History
// drawer opens on the Results side where its menu is.
func TestUIIntruderDesktopListNoticeAndHistoryAnchor(t *testing.T) {
	css := readUIAsset(t, "css/desktop/panel-intruder.css")
	requireUIContains(t, css, "#intrHistory{position:absolute;inset:0 0 0 auto;", ".intr-pl textarea{min-height:96px}")
	if strings.Contains(css, "inset:0 auto 0 0") {
		t.Error("the Intruder History drawer must anchor to the Results side, not the setup column")
	}
	if i := strings.Index(css, ".intr-pl-note:not(:empty)"); i < 0 || strings.Contains(css[i:i+260], "text-overflow:ellipsis") {
		t.Error("the large-list notice must wrap, not ellipsize")
	}
	js := executableJS(readUIAsset(t, "js/tools.js"))
	if strings.Contains(js, "· preview${") {
		t.Error("the large-list notice is too long for a 122px list column")
	}
}
