package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Report readiness is report-time information: #findBadge and the readiness
// signals on the Flight Bar's ring (the ready fraction, the arc and the blocker
// count) stay off while the operator captures and tests, and appear only while
// the Report view is open. The ring itself is always on the bar, as the way into
// the popover, and flags only missing project setup. One seam, one direction:
// report-preflight publishes the boolean through shell-hooks, ctxbar reads it.

func TestUIReportSignalPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/report-signal.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report signal node tests failed: %v\n%s", err, out)
	}
}

func TestUICtxbarGatesReadinessOnReportOpenSignal(t *testing.T) {
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, ctx,
		"shouldShowReadinessSignals(", "isReportOpen()", "onReportOpenChange(",
		"badge.classList.toggle('u-hidden'", "ringModel(s, { reportOpen: isReportOpen() })",
		"text.hidden = !m.text", "badge.hidden = !m.badge", "ring.dataset.report",
	)
	model := executableJS(readUIAsset(t, "js/ctxbar-model.js"))
	requireUIContains(t, model, "shouldShowReadinessSignals(reportOpen, n, loaded).chip")
	if regexp.MustCompile(`\.style\.display|setAttribute\('style'`).MatchString(ctx) {
		t.Error("ctxbar must hide readiness signals with hidden/u-hidden, never inline style")
	}
}

func TestUIReportSignalSeamIsOneDirection(t *testing.T) {
	ctx := readUIAsset(t, "js/ctxbar.js")
	if strings.Contains(ctx, "report-preflight") {
		t.Error("ctxbar.js must not import or reference report-preflight.js")
	}
	rp := executableJS(readUIAsset(t, "js/report-preflight.js"))
	requireUIContains(t, rp, "setReportOpen(", "TAB_CHANGE_EVENT")
	if strings.Contains(executableJS(readUIAsset(t, "js/shell-hooks.js")), "ctxbar") {
		t.Error("shell-hooks.js must not know about ctxbar")
	}
}

func TestUIReportSignalKeepsBadgeSelectorAndHiddenByDefault(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "shell.css"), "#tab-findings #findBadge")
	idx := readUIAsset(t, "index.html")
	ring := regexp.MustCompile(`<button[^>]*id="ctxRing"[^>]*>`).FindString(idx)
	if ring == "" {
		t.Fatal("#ctxRing missing")
	}
	// Outside Report the ring is the quiet one: no report signal in the static markup.
	if !strings.Contains(ring, `data-report="false"`) {
		t.Error("#ctxRing must start outside report time (data-report=false)")
	}
	for _, id := range []string{"ctxRingText", "ctxRingBadge"} {
		el := regexp.MustCompile(`<span id="` + id + `"[^>]*>`).FindString(idx)
		if !regexp.MustCompile(`\shidden[\s>]`).MatchString(el) {
			t.Errorf("#%s must be hidden by default via the hidden attribute: %s", id, el)
		}
		if regexp.MustCompile(`\sstyle=`).MatchString(el) {
			t.Errorf("#%s must not use an inline style", id)
		}
	}
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	if !strings.Contains(css, `.ctx-ring[data-state="blocked"]`) {
		t.Error("the blocked ring state must be styled")
	}
}

func TestUIReportDiscoverabilityWithoutBlockersChip(t *testing.T) {
	// The blockers palette entry must route to Report, not click a hidden chip.
	cmdk := executableJS(readUIAsset(t, "js/cmdk-actions.js"))
	if strings.Contains(cmdk, "clickLater('#ctxBlockers')") {
		t.Error("palette 'Open report blockers' must open the Report view, not click the hidden chip")
	}
}
