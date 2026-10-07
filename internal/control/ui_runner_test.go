package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The collection runner view: pure logic under node, DOM-shaped behaviour
// pinned statically against the embedded assets.

func TestUIRunnerAndCollectionsModelsUnderNode(t *testing.T) {
	node := requireNode(t)
	for _, f := range []string{"_js-tests/runner-model.test.mjs", "_js-tests/collections-model.test.mjs"} {
		cmd := exec.Command(node, "--test", f)
		cmd.Dir = "ui"
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v\n%s", f, err, out)
		}
	}
}

func TestUIRunnerViewUsesAsyncRunnerAPI(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/runner.js"))
	for _, want := range []string{
		"'/api/runner/runs'",                              // start
		"'/abort'", "'/pause'", "'/resume'", "'/persist'", // controls and the persist-ask answer
		"registerSseHandler('collrun'", // live events
		"?since=",                      // incremental results
		"applyProgress", "applyEvent",  // pure reducers from runner-model.js
	} {
		if !strings.Contains(src, want) {
			t.Errorf("runner.js does not use %s", want)
		}
	}
	if strings.Contains(src, "'/api/collections/run'") {
		t.Error("runner.js still calls the synchronous /api/collections/run; it must follow a server-owned run")
	}
}

func TestUIRunnerViewMarkupGuards(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/runner.js"))
	if regexp.MustCompile(`\.(outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
		t.Error("runner.js assigns HTML; build nodes with textContent")
	}
	// The only innerHTML is the sprite icon markup produced by icon().
	for _, m := range regexp.MustCompile(`[^\n]*\.innerHTML\s*=[^\n]*`).FindAllString(src, -1) {
		if !strings.Contains(m, "icon(") {
			t.Errorf("runner.js assigns innerHTML from a non-icon value: %s", strings.TrimSpace(m))
		}
	}
	if strings.Contains(src, "style=\"") || strings.Contains(src, ".style.") || strings.Contains(src, "cssText") {
		t.Error("runner.js writes inline style; use classes")
	}
	if regexp.MustCompile(`[\x{2190}-\x{21FF}\x{2600}-\x{27BF}\x{1F300}-\x{1FAFF}]`).MatchString(src) {
		t.Error("runner.js uses a glyph or emoji instead of a sprite icon")
	}
	for _, want := range []string{`setAttribute('role', 'status')`, `'aria-live', 'polite'`, `'aria-pressed'`, "'Abort'", "'Pause'", "'Resume'", "'Keep changes'", "'Discard changes'"} {
		if !strings.Contains(src, want) {
			t.Errorf("runner.js is missing accessible control/label %s", want)
		}
	}
	index := readUIAsset(t, "index.html")
	if !strings.Contains(index, `src="/js/runner.js"`) {
		t.Error("index.html does not load runner.js")
	}
	css := readUIAsset(t, "css/collections.css")
	for _, want := range []string{".runner-view", ".run-persist-ask", ".chip.run-fail", ".run-progress"} {
		if !strings.Contains(css, want) {
			t.Errorf("collections.css lacks the runner rule %s", want)
		}
	}
}

func TestUIRunnerModelNeverRendersUnsupportedAsPass(t *testing.T) {
	src := readUIAsset(t, "js/runner-model.js")
	if !strings.Contains(src, "'unsupported'") || !strings.Contains(src, "unsupported: 'Unsupported'") {
		t.Error("runner-model.js must give unsupported its own state and label")
	}
}

func TestUIHistoryCollectionChip(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="histCollFilter"`, `aria-pressed="true"`, `#i-collection`)
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"FLAG_COLLECTION",                            // client predicate and row badge
		"q.set('collection','0')",                    // server filter when hidden
		"state.showCollection=!state.showCollection", // the chip toggles it
		"hiding collections",                         // removable active-filter chip with a text label
		"COLL",                                       // row tag
	)
	if !strings.Contains(readUIAsset(t, "js/core.js"), "FLAG_COLLECTION=512") {
		t.Error("FLAG_COLLECTION must mirror store.FlagCollection (1<<9)")
	}
}
