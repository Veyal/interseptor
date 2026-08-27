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
