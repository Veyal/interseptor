package control

import (
	"strings"
	"testing"
)

// Review fixes: small state, lifecycle and WCAG 2.5.x items.

func TestUIStaleSSEHelloAndDensityListenerLifecycle(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "if(sseSource!==es)return; // a superseded source must not reset the retry state")
	core := executableJS(readUIAsset(t, "js/core.js"))
	requireUIContains(t, core, "vl.dispose=dispose;", "dispose();return;")
	ps := executableJS(readUIAsset(t, "js/project-state.js"))
	requireUIContains(t, ps, "run().catch(() => {})")
}

func TestUIFlowDrawerWidthHasSinglePointerAlternative(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="fdNarrow"`, `id="fdWide"`, `aria-label="Make the drawer narrower"`, `aria-label="Make the drawer wider"`)
	drawer := executableJS(readUIAsset(t, "js/flowdrawer.js"))
	requireUIContains(t, drawer, "applyWidth(width - 64, true)", "applyWidth(width + 64, true)")
	css := readUIAsset(t, "flow.css")
	requireUIContains(t, css, ".flow-drawer-sash::after{content:\"\";position:absolute;top:0;bottom:0;left:calc(var(--sp-4) * -1);right:calc(var(--sp-4) * -1)}",
		`.flow-drawer[data-mode="overlay"] .flow-drawer-width{display:none}`)
}

func TestUISheetHandleAnnouncesDetent(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/sheet.js"))
	requireUIContains(t, src,
		"live.setAttribute('role', 'status')",
		"entry.live.textContent = DETENT_TEXT[current] || ''",
		"half: 'Panel half height'",
	)
	if strings.Count(src, "sheet.append(handle, head, body, live)") != 1 {
		t.Error("the live region must be part of every sheet")
	}
}
