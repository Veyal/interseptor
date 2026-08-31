package control

import (
	"strings"
	"testing"
)

// These contracts cover the live capture surface's state transitions. The UI
// is embedded static JavaScript, so a small source-level test is the closest
// equivalent to a compile-time contract for these paths.
func TestUICaptureHistorySourceFiltersAndReturnReconcile(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, proxy,
		"showAI",
		"q.set('manual'",
		"q.set('ai'",
		"state.showManual",
		"state.showAI",
		"flowMatchesFilters",
		"flowSortValue",
	)
	if !strings.Contains(app, "t.dataset.tab==='proxy'") || !strings.Contains(app, "renderRows()") {
		t.Fatal("returning to Proxy must reconcile rows from in-memory live state")
	}
}

func TestUISettingsRestoreHonorsSavedSection(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"localStorage.getItem('setSec')",
		"data-sec=\"'+sec+'\"",
	)
}

func TestUICaptureMutableSortUpdatesReconcileOrder(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"sortValueBefore",
		"sortValueAfter",
		"scheduleReload()",
	)
}

func TestUIInterceptSelectionLoadingAndDecodedCleanup(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, intercept,
		"heldLoadingKey",
		"showHeldLoading",
		"showHeldLoadError",
		"Retry loading held message",
		"heldRaw",
		"disabled",
		"heldDecoded",
	)
}

func TestUIActivityClearAckAndMissingDuration(t *testing.T) {
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	requireUIContains(t, activity,
		"Clearing…",
		"clear failed",
		"it.ms == null",
		"renderLoadError",
	)
}

func TestUICaptureSourceFilterEmptyStateCanRecover(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	// Source visibility toggles are filters too. When they hide the loaded set,
	// History must show the filter-empty state and its recovery action must turn
	// both sources back on rather than leaving the operator at onboarding copy.
	requireUIContains(t, proxy,
		"!state.showManual||!state.showAI",
		"state.showManual=true;state.showAI=true",
		"syncSourceFilters();",
		"syncControls();renderChips();renderTagBar();loadFlows();",
	)
}

func TestUICaptureOnboardingDoesNotDuplicateTLSDiagnosis(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	if strings.Contains(proxy, "getStartedDiagnosisHint") {
		t.Fatal("Proxy onboarding must not duplicate the TLS diagnosis banner")
	}
}

func TestUICaptureDenseHistoryNamesExposeFullStatusToAssistiveTech(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	if !strings.Contains(proxy, "c.label==='St'?'Status':c.label") {
		t.Fatal("History's compact status header must expose the full Status name")
	}
	if !strings.Contains(proxy, `sel.innerHTML='<option value="">All methods</option>'`) {
		t.Fatal("History's dynamic method filter must preserve the canonical All methods label")
	}
}

func TestUIInterceptConditionalControlsWrapAtNarrowWidths(t *testing.T) {
	css := readUIAsset(t, "app.css")
	// The on/off controls and match condition are all required to operate the
	// queue. At phone width the condition must move to a full-width row; an
	// off-screen horizontally scrolling form makes the pattern field invisible.
	requireUIContains(t, css,
		"@media (max-width:720px)",
		".icpt-bar{flex-wrap:wrap;overflow-x:visible}",
		".icpt-cond{flex:1 0 100%;min-width:0}",
	)
}

func TestUIIntruderResultActionsWrapAtMediumWidths(t *testing.T) {
	css := readUIAsset(t, "app.css")
	// With the desktop navigation rail present, a 1024px viewport leaves the
	// split result pane too narrow for filters, promotion, and History on one
	// line. Keep those actions in the pane instead of widening the document.
	requireUIContains(t, css,
		"@media (max-width:1100px)",
		".intr-results .pane-head{height:auto;flex-wrap:wrap;overflow:visible}",
		".intr-results .pane-head>.spacer{display:none}",
	)
}
