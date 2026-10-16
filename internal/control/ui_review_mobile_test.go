package control

import (
	"regexp"
	"testing"
)

// Review fixes: safe-area insets, 44px targets and popover placement on phones.

func TestUIDockSafeAreaInsetsAreNotSwapped(t *testing.T) {
	css := readUIAsset(t, "mobile.css")
	requireUIContains(t, css, "padding:0 var(--safe-r) var(--safe-b) var(--safe-l)")
	if regexp.MustCompile(`padding:0 var\(--safe-l\) var\(--safe-b\) var\(--safe-r\)`).MatchString(css) {
		t.Error("padding is top/right/bottom/left: --safe-l belongs in the fourth slot")
	}
}

func TestUISegmentButtonsReach44pxOnTouch(t *testing.T) {
	mobile := readUIAsset(t, "mobile.css")
	requireUIContains(t, mobile, ".dock-seg button{flex:1 1 0;min-width:0;min-height:44px}", ".dock-more .seg button{flex:1 1 auto;min-height:44px}")
	app := readUIAsset(t, "app.css")
	requireUIContains(t, app, "  .seg button{min-height:44px;padding:var(--sp-2) var(--sp-3)}")
}

func TestUISheetsAndPopoversRespectSideInsets(t *testing.T) {
	surfaces := readUIAsset(t, "surfaces.css")
	requireUIContains(t, surfaces,
		"right:max(var(--sp-2),var(--safe-r));z-index:1}",
		"padding:0 max(var(--sp-4),var(--safe-r)) var(--sp-4) max(var(--sp-4),var(--safe-l))",
	)
	shell := readUIAsset(t, "shell.css")
	requireUIContains(t, shell, "right:max(var(--sp-2),var(--safe-r));z-index:var(--z-popover)", "  .ctx-pop{left:max(var(--sp-2),var(--safe-l));right:max(var(--sp-2),var(--safe-r));width:auto}")
	proxy := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, proxy, `html[data-soft-keyboard="true"] .proxy-filters-pop{bottom:0}`, "left:var(--safe-l);right:var(--safe-r);bottom:calc(var(--bottomnav-h) + var(--safe-b))")
}
