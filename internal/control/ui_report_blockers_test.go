package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Report readiness is report-time information: #findBadge and the blockers chip
// stay hidden while the operator captures and tests, and appear only while the
// Report view is open. One seam, one direction: report-preflight publishes the
// boolean through shell-hooks, ctxbar reads it.

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
		"badge.classList.toggle('u-hidden'", "chip.hidden",
	)
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
	m := regexp.MustCompile(`<button[^>]*id="ctxBlockers"[^>]*>`).FindString(idx)
	if m == "" {
		t.Fatal("#ctxBlockers missing")
	}
	if !regexp.MustCompile(`\shidden[\s>]`).MatchString(m) {
		t.Error("#ctxBlockers must be hidden by default via the hidden attribute")
	}
	if regexp.MustCompile(`\sstyle=`).MatchString(m) {
		t.Error("#ctxBlockers must not use an inline style")
	}
}

func TestUIReportDiscoverabilityWithoutBlockersChip(t *testing.T) {
	// The blockers palette entry must route to Report, not click a hidden chip.
	cmdk := executableJS(readUIAsset(t, "js/cmdk-actions.js"))
	if strings.Contains(cmdk, "clickLater('#ctxBlockers')") {
		t.Error("palette 'Open report blockers' must open the Report view, not click the hidden chip")
	}
}
