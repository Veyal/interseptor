package control

import (
	"strings"
	"testing"
)

// Round 3 (a5) regressions that only show in the DOM; each pins the source
// shape that fixed it, and the browser run is recorded in the change notes.

// Cancelling the first Diff prompt must not open the second one. An empty OK is
// a valid answer (the first example), so uiPrompt gets allowEmpty and the
// caller tells ” from null.
func TestDiffExampleCancelDoesNotOpenSecondPrompt(t *testing.T) {
	src := readUIAsset(t, "js/collections-matrix.js")
	i := strings.Index(src, "async function openDiffSheet")
	if i < 0 {
		t.Fatal("openDiffSheet missing")
	}
	body := src[i:]
	first := strings.Index(body, "uiPrompt(")
	cancel := strings.Index(body, "exampleAnswer == null")
	second := strings.Index(body[first+1:], "uiPrompt(") + first + 1
	if first < 0 || cancel < first || cancel > second {
		t.Fatal("a cancelled first prompt must return before the second prompt is asked")
	}
	requireUIContains(t, readUIAsset(t, "js/core.js"), "opts.allowEmpty")
}

// A runner row blocked by scope offers the same "Add host to scope" action as Send.
func TestRunnerBlockedRowOffersAddHostToScope(t *testing.T) {
	runner := readUIAsset(t, "js/runner.js")
	requireUIContains(t, runner, "'Add host to scope'", "addableBlock(r.blockReason)", "opts.addHostForItem")
	requireUIContains(t, readUIAsset(t, "js/collections-sheets.js"), "addHostForItem: addHostForRunItem", "X.addHostToScope(url)")
	requireUIContains(t, readUIAsset(t, "js/collections-response.js"), "addHostToScope, copyCurl")
}

// The run review warns, before RUN is typed, about requests that scope or the pin will block.
func TestRunReviewWarnsAboutBlockedRequests(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "js/collections-sheets.js"), "loadBlockCheck(activePin())", "coll-runplan-blocked", "blockedText(plan)")
	requireUIContains(t, readUIAsset(t, "js/collections-core.js"), "export async function loadBlockCheck")
}

// Variables saved fires one confirmation, not a toast plus a second status line.
func TestVariablesSaveHasOneConfirmation(t *testing.T) {
	src := readUIAsset(t, "js/collections-sheets.js")
	if strings.Contains(src, "status.textContent = 'Saved'") {
		t.Fatal("the inline Saved status duplicates the Variables saved toast")
	}
}

// Unchecked checkboxes keep the visible global border: the .field input box
// styling (a 1px 8%-alpha border) must not apply to checkboxes and radios.
func TestFieldInputStyleSkipsCheckables(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "app.css"), ".field input:where(:not([type=checkbox],[type=radio]))")
	requireUIContains(t, readUIAsset(t, "css/desktop/components.css"), ".field input:where(:not([type=checkbox],[type=radio],[type=range]))")
}

// Folder rows carry an accessible name; the narrow run table keeps one fixed number column.
func TestFolderRowsAndRunRowsAlignment(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "js/collections-tree.js"), "M.folderLabel(it, r.count)")
	requireUIContains(t, readUIAsset(t, "css/collections.css"), "grid-template-columns:2.5rem auto auto minmax(0,1fr) auto", "grid-template-columns:1.75rem minmax(0,1fr) auto auto")
}
