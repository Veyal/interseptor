package control

import (
	"strings"
	"testing"
)

func TestUISetupScopeMutationOwnsWizardNavigation(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/setup.js"))
	requireUIContains(t, src,
		"function setSetupNavigationBusy(busy)",
		"setSetupNavigationBusy(setupActionBusy)",
		"button.removeAttribute('aria-busy')",
		"scopeInput.disabled = setupActionBusy",
		"scopeInput.setAttribute('aria-busy', 'true')",
		"input.value.trim() === host",
		"function requestCloseSetup()",
		"onEscape: requestCloseSetup",
		"onDismiss: requestCloseSetup",
		"if (setupActionBusy) return;",
		"button.isConnected",
		"id=\"setupScopeMsg\" class=\"hint\" role=\"status\" aria-live=\"polite\"",
		"wait for the current setup action to finish",
	)
	if strings.Contains(src, "finish adding the scope host before closing setup") {
		t.Fatal("shared setup busy feedback must describe every pending setup action")
	}
	scopeStart := strings.Index(src, "$('#setupScopeAdd').onclick = async () =>")
	if scopeStart < 0 {
		t.Fatal("setup scope action boundary not found")
	}
	scopeEnd := strings.Index(src[scopeStart:], "} else {")
	if scopeEnd <= 0 {
		t.Fatal("setup scope action end not found")
	}
	requireUIContains(t, src[scopeStart:scopeStart+scopeEnd],
		"setSetupActionBusy(button, true, 'Adding…')",
		"setSetupActionBusy(button, false, label)",
		"toast(e.message,'error')",
	)
	for _, control := range []string{"setupNext", "setupBack", "setupSkip"} {
		if !strings.Contains(src, control) {
			t.Errorf("setup navigation control %s is not wired", control)
		}
	}
}

func TestUIAllowlistLoadFailureHasPersistentRetryState(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/apipanel.js"))
	requireUIContains(t, src,
		"function allowlistLoadState()",
		"let allowlistLoaded=false",
		"renderLoadError(loadState,'Allowlist'",
		"parent.insertBefore(loadState,table)",
		"data-allowlist-stale",
	)
	if strings.Contains(src, "catch(e){if(epoch!==allowlistLoadEpoch)return;toast(e.message||'allowlist failed');}") {
		t.Fatal("allowlist load failures must remain visible with a retry action")
	}
}

func TestUIAllowlistMutationFailureReconcilesInvalidatedLoad(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/apipanel.js"))
	create := strings.Index(src, "async function createAllowEntry()")
	reconcile := strings.Index(src, "await loadAllowlist();")
	if create < 0 || reconcile < 0 || reconcile < create {
		t.Fatal("a failed allowlist mutation must reconcile an invalidated GET")
	}
	if !strings.Contains(src[create:], "allowlistLoadEpoch++") {
		t.Fatal("allowlist mutations must invalidate an in-flight GET before writing")
	}
	requireUIContains(t, src,
		"showAllowlistMutationError(error)",
		"await loadAllowlist();",
		"Review the values and try the action again.",
		"Refresh list",
		"toast(e.message,'error')",
	)
}

func TestUISetupAsyncReadsOwnTheirRenderedStepAndNode(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/setup.js"))
	requireUIContains(t, src,
		"let setupSystemProxyEpoch = 0",
		"let setupReadinessEpoch = 0",
		"const systemProxyEpoch=setupSystemProxyEpoch",
		"systemProxyEpoch===setupSystemProxyEpoch",
		"const readinessNode=box",
		"readinessNode.isConnected",
		"readinessStep",
	)
}

func TestUISetupStepNavigationAndControlsRemainAccessible(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/setup.js"))
	requireUIContains(t, src,
		"const next=$('#setupNext')",
		"next.disabled=false",
		`aria-label="Copy trust command"`,
		`aria-label="Scope host"`,
		`id="setupReadiness" class="evidence" role="status" aria-live="polite" aria-atomic="true"`,
	)

	reset := strings.Index(src, "next.disabled=false")
	trust := strings.Index(src, "if (step === 1)")
	if reset < 0 || trust < 0 || reset > trust {
		t.Fatal("setup must reset Next before applying the CA-step trust gate")
	}
}
