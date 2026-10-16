package control

import "testing"

// Review fixes for the Intercept panel: a due deferred drop must not clobber an
// in-flight Forward, and the held header reports out-of-scope auto-forwards.

func TestUIInterceptDropWaitsForInFlightHeldAction(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src, "busy:()=>!!heldActionInFlight")
	model := executableJS(readUIAsset(t, "js/intercept-model.js"))
	requireUIContains(t, model, "busy = () => false", "if (busy()) { arm(key, item, retryDelay); return; }")
}

func TestUIInterceptShowsOutOfScopeAutoForwardTally(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="heldAutoFwd"`)
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src, "registerSseHandler('flow.new'", "isAutoForwarded(", "autoForwardText(autoFwd.count())", "c.hasInclude&&c.evaluable")
	model := executableJS(readUIAsset(t, "js/intercept-model.js"))
	requireUIContains(t, model, "'Out of scope, auto-forwarded: '", "NON_PROXY_FLAGS")
}
