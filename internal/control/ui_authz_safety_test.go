package control

import (
	"strings"
	"testing"
)

func TestUIAuthorizationLocksModeDuringRun(t *testing.T) {
	authz := executableJS(readUIAsset(t, "js/authz.js"))
	for _, contract := range []string{
		"const mode=$('#authzMode')",
		"mode.setAttribute('aria-busy'",
		"mode.querySelectorAll('button').forEach",
		"button.disabled=authzActionBusy",
	} {
		if !strings.Contains(authz, contract) {
			t.Errorf("Authorization run-mode lock contract missing %q", contract)
		}
	}
}

func TestUIAuthorizationAllEvidenceRowsAreKeyboardOperable(t *testing.T) {
	authz := executableJS(readUIAsset(t, "js/authz.js"))
	if !strings.Contains(authz, "function wireAuthzFlowRows(box)") {
		t.Fatal("shared authorization evidence-row wiring helper missing")
	}
	if got := strings.Count(authz, "wireAuthzFlowRows(box)"); got < 4 {
		t.Errorf("authorization evidence wiring used %d times, want helper plus selected, bulk, and cross-host result paths", got)
	}
	if !strings.Contains(authz, "wireRowKey(el,go)") {
		t.Error("authorization evidence rows must support Enter/Space activation")
	}
}
