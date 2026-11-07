package control

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// WP10 (UI overhaul): Intercept and Playbooks as workbench panels. Pure logic runs
// under node; DOM-shaped behaviour is pinned statically against the embedded
// assets, as for the other ui_* tests.

func uiRegion(t *testing.T, name string) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	open := "<!-- region:" + name + " -->"
	end := "<!-- /region:" + name + " -->"
	if strings.Count(index, open) != 1 || strings.Count(index, end) != 1 {
		t.Fatalf("region:%s markers must each appear exactly once", name)
	}
	return index[strings.Index(index, open):strings.Index(index, end)]
}

func TestUIInterceptScannerPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/intercept-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("intercept node tests failed: %v\n%s", err, out)
	}
}

func TestUIInterceptPureModulesAreImportFreeAndStyleFree(t *testing.T) {
	for _, name := range []string{"js/intercept-model.js"} {
		src := readUIAsset(t, name)
		if regexp.MustCompile(`(?m)^import `).MatchString(src) {
			t.Errorf("%s must have no imports so it runs under node", name)
		}
	}
	for _, name := range []string{"js/intercept-model.js", "js/held-undo.js"} {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "cssText") || strings.Contains(src, ".style.") {
			t.Errorf("%s writes inline style; use classes", name)
		}
	}
}

func TestUIInterceptRegionKeepsIdsAndAddsWorkbenchControls(t *testing.T) {
	region := uiRegion(t, "intercept")
	requireUIContains(t, region,
		`id="panel-intercept"`, `id="interceptToggle"`, `id="respInterceptToggle"`,
		`id="interceptFilterOn"`, `id="interceptFilterTarget"`, `id="interceptFilterPattern"`,
		`id="heldList"`, `id="heldTotal"`, `id="heldEditor"`, `id="heldRaw"`, `id="heldDecoded"`, `id="heldEmpty"`,
		`id="heldBeautifyBtn"`, `id="heldResetBtn"`, `id="heldDecodeBtn"`, `id="forwardBtn"`, `id="dropBtn"`,
		`id="rulesBody"`, `id="addRuleBtn"`, `id="interceptWarning"`,
		// workbench additions
		`id="icptWork"`, `class="icpt-work split"`, `class="icpt-queue split-list"`, `class="icpt-main split-detail"`,
		`id="heldCtx"`, `id="heldScopeText"`, `id="heldIdentityText"`, `id="heldScopeNote"`,
		`id="heldActions"`, `role="group" aria-label="Held message actions"`,
		`id="forwardRepeaterBtn"`, `id="forwardAllBtn"`, `id="heldActionStatus"`, `role="status"`,
		`id="rulesCount"`, `<details class="icpt-mr">`, `title="Drop (D)"`, `title="Forward (F)"`,
	)
	if strings.Contains(region, "style=") {
		t.Error("the intercept region must not use inline style attributes")
	}
	for _, glyph := range []string{"▶", "↗"} {
		if strings.Contains(region, glyph) {
			t.Errorf("the intercept region still uses the glyph %s as an icon", glyph)
		}
	}
	// The action buttons follow the editor in DOM order, so a keyboard user
	// meets them after the message they act on.
	if strings.Index(region, `id="heldRaw"`) > strings.Index(region, `id="forwardBtn"`) {
		t.Error("Forward/Drop must come after the held message editor in DOM order")
	}
}

func TestUIInterceptDropIsDeferredWithUndoAndNeverClaimsAnUndoOfASentDrop(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"$('#dropBtn').onclick=scheduleDrop;",
		"createDropScheduler({",
		"performDrop(entry);",
		"async function performDrop(sel)",
		"if(heldSelectionOwns(actionKey))setHeldActionState(button,'pending','Dropping…')",
		"onPause:()=>dropScheduler.pause(key)",
		"onResume:()=>dropScheduler.resume(key)",
		"kept on hold",
	)
	if n := strings.Count(src, "performDrop("); n != 2 {
		t.Errorf("performDrop must be defined once and called only by the scheduler commit, found %d references", n)
	}
	// The irreversible API call lives only in performDrop.
	if n := strings.Count(src, "'/drop'"); n != 1 {
		t.Errorf("exactly one drop API call site expected, found %d", n)
	}
	start := strings.Index(src, "function scheduleDrop()")
	end := strings.Index(src, "$('#dropBtn').onclick=scheduleDrop;")
	if start < 0 || end < start {
		t.Fatal("scheduleDrop not found")
	}
	body := src[start:end]
	if strings.Contains(body, "uiConfirm(") || strings.Contains(body, "api(") {
		t.Error("Drop must not open a confirm dialog or call the API directly; it is deferred behind Undo")
	}
	if strings.Contains(strings.ToLower(src), "undropped") || strings.Contains(src, "drop undone") || strings.Contains(src, "restored") {
		t.Error("never claim a sent drop was undone: the API has no undo")
	}
	// The existing keyboard path stays: f and d still click the same buttons.
	app := readUIAsset(t, "js/app.js")
	requireUIRegex(t, app, `isPlainShortcut\(e,'f'\)\|\|isPlainShortcut\(e,'d'\)`)
	requireUIContains(t, app, "$(e.key.toLowerCase()==='d'?'#dropBtn':'#forwardBtn').click()")
}

func TestUIInterceptKeepsHeldBadgeAndScopeHonesty(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"const badge=$('#heldBadge');if(badge){badge.style.display=total?'inline-block':'none';badge.textContent=total;}",
		"scopeNote(icptCtx.scope).text",
		"createSplitPane({",
		"icptCtx.pane.showDetail(row)",
		"icptCtx.pane.showList()",
		"syncRulesCount();",
	)
	model := readUIAsset(t, "js/intercept-model.js")
	// The server exposes no out-of-scope counter. The tally is derived from the live
	// flow stream with the exact client scope mirror, counts only proxy-captured
	// flows and hides itself when scope is undecidable (regex rules); it never
	// calls an API for a number.
	if regexp.MustCompile(`(?i)outOfScopeCount|api\([^)]*auto-?forward`).MatchString(src + model) {
		t.Error("the auto-forward tally must come from the flow stream, not an invented API field")
	}
	requireUIContains(t, model, "if (!interceptOn || !flow || !compiled || !compiled.evaluable || !compiled.hasInclude) return false;")
}

func TestUIInterceptStylesheetContract(t *testing.T) {
	const sheet = "panel-intercept-workbench.css"
	css := readUIAsset(t, sheet)
	if len(strings.TrimSpace(css)) == 0 {
		t.Fatal("intercept workbench stylesheet is empty")
	}
	requireUIContains(t, readUIAsset(t, "index.html"), `<link rel="stylesheet" href="/panel-intercept-workbench.css">`)
	if regexp.MustCompile(`(?i)\btransition\s*:|@keyframes|\banimation\s*:`).MatchString(css) {
		t.Error("intercept stylesheet must not animate")
	}
	if strings.Contains(css, "@import") || strings.Contains(css, "url(http") {
		t.Error("intercept stylesheet must be self-contained")
	}
	defined := map[string]bool{}
	for _, name := range []string{"app.css", "workbench.css", "primitives.css", "shell.css", "flow.css", "mobile.css", "surfaces.css"} {
		for _, m := range regexp.MustCompile(`(--[A-Za-z0-9-]+)\s*:`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			defined[m[1]] = true
		}
	}
	for _, m := range regexp.MustCompile(`var\((--[A-Za-z0-9-]+)`).FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] {
			t.Errorf("%s uses undefined custom property %s", sheet, m[1])
		}
	}
	requireUIContains(t, css, ".icpt-autofwd{", ".icpt-actionbar{", "position:sticky;bottom:0", ".icpt-actionbar .btn:focus-visible", "@media (max-width:720px)")
	for _, m := range regexp.MustCompile(`min-width:\s*(\d+)px`).FindAllStringSubmatch(css, -1) {
		if px, _ := strconv.Atoi(m[1]); px > 375 {
			t.Errorf("%s has min-width %spx, wider than 375px", sheet, m[1])
		}
	}
}

func TestUIInterceptForwardAllAndForwardToRepeaterReuseExistingEndpoints(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"forwardAllPlan(heldItems(),{dropping:dropMarks,rawCache:heldRawCache,originalCache:heldOriginalCache,binary:new Set(heldBinaryInfo.keys())})",
		"await uiConfirm('Forward all held items?'",
		"api(forwardPath(step.side,step.id),",
		"await $('#forwardBtn').onclick();",
		"if(!heldItem(sel.id,'req'))sendRawToRepeater(sent);",
	)
	model := readUIAsset(t, "js/intercept-model.js")
	requireUIContains(t, model, "'/api/intercept/response/'", "'/api/intercept/'")
	if strings.Contains(model, "/api/intercept/all") {
		t.Error("forward-all must call the per-item forward endpoint, not an invented bulk endpoint")
	}
}
