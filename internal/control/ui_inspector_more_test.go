package control

import (
	"regexp"
	"strings"
	"testing"
)

// The inspector's "More" button opened the flow context menu and then had it
// closed again by the app-wide click-to-close listener in the same bubbling
// click, so the menu was built (15 items) but never shown. The row overflow
// button (.rowOverflow[data-rowmenu]) does not have this bug because it
// registers in the capture phase and calls stopImmediatePropagation first.
//
// Reproduced in headless Chrome before the fix: dispatching a bubbling click
// on #inspectMoreActions left #ctxmenu at display:none with 15 items, while
// calling its onclick directly (no bubbling) showed the menu.
func TestUIInspectorMoreButtonSurvivesClickToClose(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/proxy.js"))

	start := strings.Index(js, "inspectMoreActions.onclick")
	if start < 0 {
		t.Fatal("proxy.js no longer wires #inspectMoreActions.onclick")
	}
	end := strings.Index(js[start:], "};")
	if end < 0 {
		t.Fatal("could not bound the inspectMoreActions handler")
	}
	handler := js[start : start+end]

	if !strings.Contains(handler, "stopPropagation()") {
		t.Error("the #inspectMoreActions handler must stop the click from reaching the app-wide " +
			"click-to-close listener, or the menu it opens is hidden again in the same event")
	}
	// The guard is worthless after the menu is opened.
	if sp, show := strings.Index(handler, "stopPropagation()"), strings.Index(handler, "showCtx("); sp > show {
		t.Error("stopPropagation() must run before showCtx(), not after")
	}

	// The listener this guards against must still be the reason the guard exists;
	// if it is ever removed, this test should be revisited rather than silently pass.
	closer := regexp.MustCompile(`document\.addEventListener\('click',\s*e=>\{if\(!ctx\.contains\(e\.target\)\)hideCtx`)
	if !closer.MatchString(js) {
		t.Error("the app-wide click-to-close listener changed shape; re-verify the More button guard")
	}

	// The row overflow button keeps its own, different guard.
	if !strings.Contains(js, "e.stopImmediatePropagation()") {
		t.Error("the row overflow menu lost its capture-phase stopImmediatePropagation guard")
	}
}
