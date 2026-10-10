package control

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The identity matrix used to fire every request of a collection against
// every saved identity from the command palette with no question asked. These
// guards pin that the only path to /api/collmatrix/run goes through the run
// plan gate from collections-run-plan.js.

func TestUIMatrixRunIsGatedByRunPlan(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/collections-matrix.js"))
	const runURL = "/api/collmatrix/run"

	if n := strings.Count(src, runURL); n != 1 {
		t.Fatalf("collections-matrix.js references %s %d times; want exactly one call site behind the gate", runURL, n)
	}
	if !regexp.MustCompile(`from\s+'\./collections-run-plan\.js'`).MatchString(src) {
		t.Error("collections-matrix.js does not import collections-run-plan.js")
	}
	for _, want := range []string{"buildRunPlan(", "planSummary(", "methodBreakdown(", "planConfirmed("} {
		if !strings.Contains(src, want) {
			t.Errorf("collections-matrix.js does not use %s", want)
		}
	}

	// The function that owns the fetch must check the gate before the fetch.
	at := strings.Index(src, runURL)
	start := strings.LastIndex(src[:at], "function ")
	if start < 0 {
		t.Fatal("cannot find the function that calls " + runURL)
	}
	head := src[start:at]
	if !strings.Contains(head, "planConfirmed(") {
		t.Errorf("the function calling %s does not check planConfirmed() before sending", runURL)
	}
	if !strings.Contains(head, "plan.empty") {
		t.Errorf("the function calling %s does not refuse an empty plan", runURL)
	}

	// Never compare against the phrase by hand; planConfirmed owns that.
	if regexp.MustCompile(`===?\s*'RUN'|CONFIRM_PHRASE\s*===?|===?\s*CONFIRM_PHRASE`).MatchString(src) {
		t.Error("collections-matrix.js compares the confirm phrase itself; use planConfirmed()")
	}

	// The command palette may only open the review sheet, never run directly.
	if regexp.MustCompile(`run:\s*runMatrix`).MatchString(src) {
		t.Error("a command runs runMatrix directly, bypassing the review sheet")
	}
	// Every call of runMatrix must hand it a plan.
	for _, m := range regexp.MustCompile(`runMatrix\(([^)]*)\)`).FindAllStringSubmatch(src, -1) {
		if !strings.HasPrefix(strings.TrimSpace(m[1]), "plan") {
			t.Errorf("runMatrix(%s) is not given a plan", m[1])
		}
	}
}

func TestUIMatrixReviewStatesScopeBeforeRunning(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/collections-matrix.js"))
	for _, want := range []string{"plan.scopeLabel", "plan.identityNames", "plan.hosts", "plan.needsPhrase"} {
		if !strings.Contains(src, want) {
			t.Errorf("matrix review does not surface %s", want)
		}
	}
}

func TestUIMatrixIsTheOnlyCollmatrixRunCaller(t *testing.T) {
	files, err := os.ReadDir("ui/js")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".js") || f.Name() == "collections-matrix.js" {
			continue
		}
		if strings.Contains(executableJS(readUIAsset(t, "js/"+f.Name())), "/api/collmatrix/run") {
			t.Errorf("%s calls /api/collmatrix/run outside the gated matrix module", f.Name())
		}
	}
}

// A bulk run is the one action in this app that cannot be undone: it sends live
// requests, DELETEs included, at someone else's system. The runner view carries
// its own Run button, so the run sheet's pre-run review is not enough on its
// own -- the refusal has to sit at the POST. It must also be fail-closed: a
// caller that mounts the view without a confirmation step gets a refusal, not a
// silent send.
func TestUIRunnerRefusesToSendWithoutConfirmation(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/runner.js"))

	start := strings.Index(src, "async function run(")
	if start < 0 {
		t.Fatal("runner.js no longer defines run(); re-check where the gate belongs")
	}
	post := strings.Index(src[start:], "'/api/runner/runs'")
	if post < 0 {
		t.Fatal("run() no longer POSTs to /api/runner/runs")
	}
	gate := src[start : start+post]

	if !strings.Contains(gate, "opts.confirmRun") {
		t.Error("run() must consult opts.confirmRun before POSTing a run; without it the " +
			"runner view's own Run button is an ungated bulk send")
	}
	if !strings.Contains(gate, `typeof opts.confirmRun !== 'function'`) {
		t.Error("the confirmation must be fail-closed: a runner mounted without confirmRun has to " +
			"refuse, not send")
	}

	// The sheet that mounts the runner has to supply it, or every run is refused.
	sheets := executableJS(readUIAsset(t, "js/collections-sheets.js"))
	if !strings.Contains(sheets, "confirmRun") {
		t.Error("collections-sheets.js mounts the runner without a confirmRun callback, so the " +
			"runner can never send")
	}
	// And it must gate on the shared model rather than its own string compare.
	if !strings.Contains(sheets, "planConfirmed") {
		t.Error("the run sheet must gate on planConfirmed from collections-run-plan.js")
	}
}
