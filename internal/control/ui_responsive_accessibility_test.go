package control

import (
	"strings"
	"testing"
)

// Phone-width workspaces must keep primary controls visible without relying on
// an undiscoverable horizontal swipe. Desktop keeps the compact, single-row
// toolbars; only the established narrow breakpoint wraps these two dense panels.
func TestUINarrowProxyAndMapToolbarsKeepControlsVisible(t *testing.T) {
	css := readUIAsset(t, "app.css")
	narrow := strings.Index(css, "@media (max-width:480px){")
	if narrow < 0 {
		t.Fatal("phone viewport media rule not found")
	}
	block := css[narrow:]
	for _, contract := range []string{
		"#panel-proxy>.toolbar:first-child",
		"#panel-proxy>div.toolbar-secondary",
		"#panel-map>.toolbar",
		"flex-wrap:wrap",
		"overflow-x:visible",
		"#mapViewSeg{flex:1 1 100%",
	} {
		if !strings.Contains(block, contract) {
			t.Errorf("phone toolbar contract missing %q", contract)
		}
	}
}

// Scanner's desktop master/detail split becomes unusably narrow on a phone.
// Stack it at the same breakpoint as the other dense workspaces so both the
// issue list and evidence detail retain a readable width.
func TestUINarrowScannerStacksMasterDetail(t *testing.T) {
	css := readUIAsset(t, "app.css")
	narrow := strings.Index(css, "@media (max-width:720px){")
	if narrow < 0 {
		t.Fatal("narrow viewport media rule not found")
	}
	block := css[narrow:]
	for _, contract := range []string{
		".scan-passive-view{flex-direction:column",
		".scan-passive-view .scan-list{width:100%",
		".scan-detail-pane{min-height:0",
	} {
		if !strings.Contains(block, contract) {
			t.Errorf("narrow Scanner contract missing %q", contract)
		}
	}
}

// Intruder payload editors are generated dynamically, so their accessible name
// cannot be supplied by index.html. Keep the runtime naming contract explicit.
func TestUIIntruderPayloadEditorsHaveAccessibleNames(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"ta.setAttribute('aria-label'",
		"Payload list for all injection positions",
		"Payload list for injection position",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder payload editor accessibility contract missing %q", contract)
		}
	}
}
