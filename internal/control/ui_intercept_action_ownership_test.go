package control

import (
	"strings"
	"testing"
)

// Forward and Drop share their action buttons with whichever held item is
// selected. Their asynchronous result must therefore be scoped to the item
// that started the request; a late failure must not repaint a newer selection.
func TestUIInterceptActionResultKeepsOriginatingSelectionOwnership(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	for _, contract := range []string{
		"function heldSelectionOwns(key)",
		"function clearHeldActionState(button,label)",
		"function restoreHeldActionControls()",
		"function setHeldActionResult(button,key,stateName,label)",
		"if(!heldSelectionOwns(key))return false",
		"clearHeldActionState($('#forwardBtn'),'Forward')",
		"clearHeldActionState($('#dropBtn'),'Drop')",
		"if(state.heldSel&&!heldLoadingKey)setHeldControlsDisabled(false)",
		"if(!heldActionInFlight)restoreHeldActionControls();",
		"reconcileHeldRemoval(sel);",
		"const actionKey=heldKey(sel.side,sel.id)",
		"setHeldActionResult(button,actionKey,'success','Forwarded')",
		"setHeldActionResult(button,actionKey,'error','Forward failed')",
		"resetHeldAction(button,'Forward',600,epoch,actionKey)",
		"resetHeldAction(button,'Forward',900,epoch,actionKey)",
		"setHeldActionResult(button,actionKey,'success','Dropped')",
		"setHeldActionResult(button,actionKey,'error','Drop failed')",
		"resetHeldAction(button,'Drop',600,epoch,actionKey)",
		"resetHeldAction(button,'Drop',900,epoch,actionKey)",
	} {
		if !strings.Contains(intercept, contract) {
			t.Errorf("Intercept action ownership contract missing %q", contract)
		}
	}
	reconcileStart := strings.Index(intercept, "function reconcileHeldRemoval(sel)")
	releaseStart := strings.Index(intercept, "function releaseHeldAction()")
	if reconcileStart < 0 || releaseStart <= reconcileStart ||
		!strings.Contains(intercept[reconcileStart:releaseStart], "restoreHeldActionControls();") {
		t.Error("acknowledged queue removal must clear stale shared action state before rendering")
	}
	selectStart := strings.Index(intercept, "export async function selectHeld(id,side,opts={})")
	if selectStart < 0 || !strings.Contains(intercept[selectStart:], "if(!heldActionInFlight)restoreHeldActionControls();") {
		t.Error("changing selection after a settled action must clear its shared result state")
	}
}
