package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP8 (UI overhaul): the Report preflight sub-view. The gating, grouping and
// URL logic is pure and runs under node; the DOM contracts are pinned statically
// against the embedded assets.

func TestUIReportPreflightPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/report-preflight-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("report preflight node tests failed: %v\n%s", err, out)
	}
}

func TestUIReportPreflightModelIsPure(t *testing.T) {
	src := readUIAsset(t, "js/report-preflight-model.js")
	code := executableJS(src)
	for _, m := range regexp.MustCompile(`(?m)^import .* from '([^']+)'`).FindAllStringSubmatch(src, -1) {
		if m[1] != "./finding-workspace.js" && m[1] != "./project-state.js" {
			t.Errorf("model imports %s; only the pure finding-workspace and project-state modules are allowed", m[1])
		}
	}
	if regexp.MustCompile(`\b(window|document|localStorage|fetch)\b`).MatchString(code) {
		t.Error("report-preflight-model.js touches browser globals; keep it pure")
	}
}

func TestUIReportPreflightDisplaysServerReadinessOnly(t *testing.T) {
	for _, name := range []string{"js/report-preflight.js", "js/report-preflight-model.js"} {
		code := executableJS(readUIAsset(t, name))
		// The client must not recompute readiness from finding content.
		for _, banned := range []string{".readiness.checks", "completeness", "f.blocks", "finding.blocks", "cvssScore", "evaluateCvss"} {
			if strings.Contains(code, banned) {
				t.Errorf("%s re-derives readiness (%q); display the server answer", name, banned)
			}
		}
	}
}

func TestUIReportPreflightGatesExportAndRequiresTypedOverride(t *testing.T) {
	ui := executableJS(readUIAsset(t, "js/report-preflight.js"))
	for _, want := range []string{
		`aria-disabled="${gate.allowed ? 'false' : 'true'}"`,
		`aria-describedby="reportExportReason"`,
		`id="reportExportReason"`,
		`if (exportGate(gateInput()).allowed) runExport('final')`,
		`overrideConfirmed(S.typed)`,
		`id="reportDraftType"`,
		`aria-disabled="${ok ? 'false' : 'true'}"`,
		`runExport('draft')`,
		`role="status" aria-live="polite"`,
	} {
		if !strings.Contains(ui, want) {
			t.Errorf("report-preflight.js missing gating contract %q", want)
		}
	}
	// The final export must never be reachable through a plain disabled attribute
	// (it would drop out of the tab order and hide the reason).
	if regexp.MustCompile(`id="reportExport"[^>]*\sdisabled[\s>=]`).MatchString(ui) {
		t.Error("#reportExport must use aria-disabled, not disabled")
	}
	// Draft export is only reachable through the typed confirm path.
	if n := strings.Count(ui, "runExport('draft')"); n != 1 {
		t.Errorf("runExport('draft') must have exactly one call site (the typed confirm); found %d", n)
	}
}

func TestUIReportPreflightKeepsTheExportEndpoints(t *testing.T) {
	model := executableJS(readUIAsset(t, "js/report-preflight-model.js"))
	for _, want := range []string{"/api/findings/report?format=", "&statuses=", "&mode=", "&groupBy=tag", "/api/findings/readiness?statuses="} {
		if !strings.Contains(model, want) {
			t.Errorf("model missing %q", want)
		}
	}
}

// The legacy export modal had a "Draft" option with no typed confirmation, so
// every entry point (toolbar, palette, Next chip, Export findings command) now
// opens the Report preflight view and the modal is gone.
func TestUIReportPreflightIsTheOnlyExportPath(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, id := range []string{`id="findExportModal"`, `id="findExportOpen"`, `id="findExport"`, `id="findReadinessCheck"`, `id="findReadinessBoard"`, `findExportMode`} {
		if strings.Contains(index, id) {
			t.Errorf("legacy export markup %s must be gone", id)
		}
	}
	if strings.Contains(executableJS(readUIAsset(t, "js/core.js")), "'findExportModal'") {
		t.Error("findExportModal must not stay in MODAL_IDS")
	}
	for _, name := range []string{"js/app.js", "js/cmdk-actions.js", "js/ctxbar.js", "js/cmdk-logic.js", "js/findings.js", "js/report-preflight.js"} {
		src := executableJS(readUIAsset(t, name))
		for _, gone := range []string{"findExportOpen", "findExportModal", "findReadinessCheck", "exportFindingsReport"} {
			if strings.Contains(src, gone) {
				t.Errorf("%s still references the removed legacy export %q", name, gone)
			}
		}
	}
	requireUIContains(t, executableJS(readUIAsset(t, "js/cmdk-actions.js")), "getShellApi().openReport")
	requireUIContains(t, executableJS(readUIAsset(t, "js/ctxbar.js")), "getShellApi().openReport")
	requireUIContains(t, executableJS(readUIAsset(t, "js/app.js")), "getShellApi().openReport")
	requireUIContains(t, executableJS(readUIAsset(t, "js/report-preflight.js")), "setShellApi({ openReport: openReportView })")
}

func TestUIReportPreflightMounts(t *testing.T) {
	index := readUIAsset(t, "index.html")
	if strings.Count(index, `id="findReportMount"`) != 1 {
		t.Error("#findReportMount must exist exactly once")
	}
	hooks := readUIAsset(t, "js/shell-hooks.js")
	if !strings.Contains(hooks, "'report-preflight'") {
		t.Error("report-preflight must be in the guarded optional module list")
	}
	ui := executableJS(readUIAsset(t, "js/report-preflight.js"))
	for _, want := range []string{"#findReportMount", "findReportToggle", "aria-pressed", "aria-controls", "#scanFindingsView", "projectState.subscribe", "handleAppHash"} {
		if !strings.Contains(ui, want) {
			t.Errorf("report-preflight.js missing mount contract %q", want)
		}
	}
	if !strings.Contains(index, `href="/report.css"`) {
		t.Error("index.html must link report.css")
	}
}

func TestUIReportPreflightIsAccessibleAndStyleFree(t *testing.T) {
	ui := executableJS(readUIAsset(t, "js/report-preflight.js"))
	for _, want := range []string{`aria-labelledby="reportTitle"`, `tabindex="-1"`, `role="alert"`, `aria-busy`, `aria-label="Fix `, `i-alert-tri`, `i-check-circle`} {
		if !strings.Contains(ui, want) {
			t.Errorf("report-preflight.js missing accessibility contract %q", want)
		}
	}
	if strings.Contains(ui, `style="`) || strings.Contains(ui, "cssText") || strings.Contains(ui, ".style.") {
		t.Error("report-preflight.js writes inline style; use classes")
	}
	// localStorage use must be guarded.
	if !regexp.MustCompile(`try \{[^}]*localStorage`).MatchString(ui) {
		t.Error("localStorage access must be wrapped in try/catch")
	}
	css := readUIAsset(t, "report.css")
	for _, want := range []string{"@media (max-width:900px)", "@media (max-width:720px)", "var(--hit-min)", ".rp-reason", ".rp-confirm"} {
		if !strings.Contains(css, want) {
			t.Errorf("report.css missing %q", want)
		}
	}
	if regexp.MustCompile(`@keyframes|transition\s*:|animation\s*:`).MatchString(css) {
		t.Error("report.css must not animate; the shared .skel pulse is the only motion and is covered by reduced motion")
	}
	if regexp.MustCompile(`min-width:\s*[4-9]\d\dpx`).MatchString(css) {
		t.Error("report.css must not set a min-width wider than 375px")
	}
}
