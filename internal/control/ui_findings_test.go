package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP7 (UI overhaul): the Findings workspace. Pure logic runs under node; the DOM
// contracts are pinned statically against the embedded assets.

func findingsRegion(t *testing.T, name string) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:"+name+" -->")
	end := strings.Index(index, "<!-- /region:"+name+" -->")
	if start < 0 || end < start {
		t.Fatalf("region:%s markers missing", name)
	}
	return index[start:end]
}

func TestUIFindingsPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/readiness-meter.test.mjs", "_js-tests/evidence-tray.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("findings node tests failed: %v\n%s", err, out)
	}
}

func TestUIFindingsPureModulesHaveNoImportsOrInlineStyle(t *testing.T) {
	for _, name := range []string{"js/readiness-meter.js", "js/evidence-tray.js"} {
		src := readUIAsset(t, name)
		if regexp.MustCompile(`(?m)^import `).MatchString(src) {
			t.Errorf("%s must have no imports so it runs under node", name)
		}
		code := executableJS(src)
		if strings.Contains(code, `style="`) || strings.Contains(code, "cssText") || strings.Contains(code, ".style.") {
			t.Errorf("%s writes inline style; use classes", name)
		}
		if regexp.MustCompile(`\b(window|localStorage)\b`).MatchString(code) {
			t.Errorf("%s touches browser globals; keep it pure", name)
		}
	}
}

// P1: the client displays server readiness and never decides pass or fail.
func TestUIFindingsReadinessIsDisplayedNeverDerived(t *testing.T) {
	derive := regexp.MustCompile(`\.checks\s*\.\s*(filter|every|some|reduce|find)\b|\.ok\s*[=!]==|\.stage\s*=[^=>]|\.gaps\s*\.length\s*(===|==)\s*0`)
	for _, name := range []string{"js/readiness-meter.js", "js/evidence-tray.js"} {
		if m := derive.FindString(executableJS(readUIAsset(t, name))); m != "" {
			t.Errorf("%s re-derives readiness (%q); render the server stage and gaps only", name, m)
		}
	}
	meter := readUIAsset(t, "js/readiness-meter.js")
	requireUIContains(t, meter, "STAGES", "aria-valuetext", "role: 'meter'", "Readiness unknown")
	findings := readUIAsset(t, "js/findings.js")
	requireUIContains(t, findings, "readinessMeterHTML(f.readiness", "id: 'findMeter'", "findingRowKey")
}

func TestUIFindingsMarkupContract(t *testing.T) {
	findings := findingsRegion(t, "findings")
	requireUIContains(t, findings,
		`id="findNew"`, `id="findEmptyNew"`, `id="findGuide"`, `aria-controls="findGuideAside"`,
		`id="findInlineNew"`, `id="findInlineTitle"`, `id="findInlineSeverity"`, `id="findInlineCreate"`, `id="findInlineCancel"`,
		`id="findInlineStatus"`, `id="findNewDialog"`, `id="findGuideAside"`, `id="findGuideDialog"`,
		`id="findReportMount"`, `id="findList"`, `id="findDetail"`, `id="findSearch"`, `id="findCount"`)
	if !regexp.MustCompile(`id="findInlineStatus"[^>]*role="status"[^>]*aria-live="polite"`).MatchString(findings) {
		t.Error("inline new-finding status must be a polite status region")
	}
	if !regexp.MustCompile(`<label[^>]*for="findInlineTitle"`).MatchString(findings) {
		t.Error("inline title input needs a programmatic label")
	}
	if regexp.MustCompile(`\sstyle=`).MatchString(findings) {
		t.Error("findings region must not use inline style attributes")
	}
	// findReportMount is the WP8 mount point and must stay hidden until used.
	if !regexp.MustCompile(`id="findReportMount"[^>]*hidden`).MatchString(findings) {
		t.Error("#findReportMount must start hidden")
	}
}

// Every legacy finding dialog still exists, is registered as a modal and is
// reachable from the palette (reversibility until the cleanup commit).
func TestUIFindingsLegacyDialogsStayReachable(t *testing.T) {
	modals := findingsRegion(t, "modals-findings")
	core := readUIAsset(t, "js/core.js")
	logic := readUIAsset(t, "js/cmdk-logic.js")
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"findGuideModal", "findCreateModal", "findPickModal", "findFlowPickModal"} {
		if !strings.Contains(modals, `id="`+id+`"`) {
			t.Errorf("legacy dialog %s was removed from the findings modal region", id)
		}
		if !strings.Contains(core, "'"+id+"'") {
			t.Errorf("%s dropped out of MODAL_IDS", id)
		}
		if !regexp.MustCompile(`\{ id: '` + id + `'[^\n]*\},?\n`).MatchString(logic) {
			t.Errorf("%s is not offered by the palette", id)
		}
	}
	// The palette clicks these buttons to open the dialogs now that the primary
	// controls open the inline row and the collapsible guide.
	requireUIContains(t, logic, `click: '#findNewDialog'`, `click: '#findGuideDialog'`)
	requireUIContains(t, index, `id="findNewDialog"`, `id="findGuideDialog"`)
	findings := readUIAsset(t, "js/findings.js")
	requireUIContains(t, findings, "$('#findNewDialog')", "openFindCreate", "$('#findGuideDialog')")
}

func TestUIFindingsInlineNewFindingBehaviour(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src,
		"function submitInlineNewFinding", "event.preventDefault()", "findingInlineBusy",
		"setInlineStatus('Finding title is required.', 'error')", "aria-invalid",
		"projectState.refresh({ reason: 'finding-create' })",
		"$('#findNew').onclick = showInlineNewFinding", "$('#findEmptyNew').onclick = showInlineNewFinding",
		"event.key === 'Escape'")
}

func TestUIFindingsEvidenceTrayContract(t *testing.T) {
	tray := readUIAsset(t, "js/evidence-tray.js")
	requireUIContains(t, tray,
		"data-evidence-drop", "aria-live=\"polite\"", "keyMoveDir", "e.altKey", "announceMove", "announceRemove",
		"showUndoToast", "UNDO_MS = 5000", "data-et-move", "data-et-remove")
	findings := readUIAsset(t, "js/findings.js")
	requireUIContains(t, findings, "evidenceTrayHTML(bodyBlocks", "wireEvidenceTray(", `id="findEvidenceTray"`, "renderEvidenceTray(fid, true)", "scheduleSave(fid)")
	// Attaching by drag uses the WP5 delegated handler through the same attributes.
	attach := readUIAsset(t, "js/evidence-attach.js")
	requireUIContains(t, attach, "[data-evidence-drop]", "z.dataset.findingId")
	requireUIContains(t, findings, `data-evidence-drop data-finding-id="${f.id}"`)
	// Move and remove must stay reachable by buttons as well as by keys.
	if !strings.Contains(tray, `aria-label="Move earlier:`) || !strings.Contains(tray, `aria-label="Remove from evidence:`) {
		t.Error("tray tiles need labelled move and remove buttons (keyboard and pointer alternative to drag)")
	}
}

// Rows are keyed so an SSE nudge that changes nothing visible does not rebuild
// the list (500 findings must not repaint on every event).
func TestUIFindingsListUsesKeyedRender(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src, "function findingRowKey(f)", "box.dataset.listKey !== listKey", "if (rowsChanged) box.querySelectorAll('.find-row')", "delete box.dataset.listKey")
	start := strings.Index(src, "function findingRowKey(f)")
	end := strings.Index(src[start:], "function renderFindings()")
	for _, field := range []string{"f.id", "f.title", "f.severity", "f.status", "f.target", "findingPocCount(f)", "r.stage", "r.gaps"} {
		if !strings.Contains(src[start:start+end], field) {
			t.Errorf("row key omits %s, so a visible change would be skipped", field)
		}
	}
}

func TestUIFindingsAdoptsSplitPaneForDesktop(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src, "import { createSplitPane } from './split.js'", "createSplitPane({ root, list, detail, key: 'findings-split'", "scopeKey: projectStorageKey", "stackBelow: 0")
	css := readUIAsset(t, "findings.css")
	requireUIContains(t, css, "#scanFindingsView.split{display:grid}", "#scanFindingsView.split.is-empty>.split-sash{display:none}")
	mobile := css[strings.Index(css, "@media(max-width:720px){\n  #scanFindingsView.split"):]
	requireUIContains(t, mobile[:400], "#scanFindingsView.split>.split-sash{display:none}")
	// The old class names stay on the split children for existing tests and styles.
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `class="find-browser"`, `id="findDetail" class="find-detail"`)
}

func TestUIFindingsAutosaveAndReadinessCheckStates(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src, "'Unsaved changes'", "'Saving…'", "'Saved'", "'Save failed'")
	// Readiness checks live in the Report preflight view ("Check again"); the old
	// export modal and its board are gone.
	requireUIContains(t, executableJS(readUIAsset(t, "js/report-preflight.js")), "reportRecheck", "readinessURL(")
}

// WP7 stylesheet rules animate nothing, define their focus states and meet the
// touch-target tier; every token they read exists.
func TestUIFindingsStylesheetRules(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	at := strings.Index(css, "Findings workspace (UI overhaul WP7)")
	if at < 0 {
		t.Fatal("WP7 findings styles missing")
	}
	wp7 := css[at:]
	if regexp.MustCompile(`transition|@keyframes|animation`).MatchString(wp7) {
		t.Error("WP7 findings styles must not animate; the sash and detail motion live in primitives.css")
	}
	requireUIContains(t, wp7, ".rm-seg", ".rm-compact", ".et-tile:focus-visible", "a.rm-seg:focus-visible", "(pointer:coarse)", "(forced-colors:active)", "min-height:24px")
	declared := parseThemeBlock(t, readUIAsset(t, "app.css"), ":root{")
	for _, m := range regexp.MustCompile(`var\((--[a-zA-Z0-9-]+)[,)]`).FindAllStringSubmatch(wp7, -1) {
		if _, ok := declared[m[1]]; !ok {
			t.Errorf("WP7 findings styles read undeclared token %s", m[1])
		}
	}
	if regexp.MustCompile(`font-size:\s*[0-9.]+px`).MatchString(wp7) {
		t.Error("WP7 findings styles must use the --fs-* scale")
	}
}

func TestUIFindingsChangelogEntry(t *testing.T) {
	log := readRepoFile(t, "CHANGELOG.md")
	i := strings.Index(log, "## [Unreleased]")
	if i < 0 || !strings.Contains(log[i:], "Findings workspace (UI overhaul WP7)") {
		t.Error("CHANGELOG.md [Unreleased] needs a WP7 Findings workspace entry")
	}
}
