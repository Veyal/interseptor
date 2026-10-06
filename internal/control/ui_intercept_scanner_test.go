package control

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// WP10 (UI overhaul): Intercept and Scanner as workbench panels. Pure logic runs
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
	cmd := exec.Command(node, "--test", "_js-tests/intercept-model.test.mjs", "_js-tests/scanner-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("intercept/scanner node tests failed: %v\n%s", err, out)
	}
}

func TestUIInterceptScannerPureModulesAreImportFreeAndStyleFree(t *testing.T) {
	for _, name := range []string{"js/intercept-model.js", "js/scanner-model.js"} {
		src := readUIAsset(t, name)
		if regexp.MustCompile(`(?m)^import `).MatchString(src) {
			t.Errorf("%s must have no imports so it runs under node", name)
		}
	}
	for _, name := range []string{"js/intercept-model.js", "js/scanner-model.js", "js/held-undo.js"} {
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

func TestUIScannerRegionKeepsIdsAndAddsWorkbenchControls(t *testing.T) {
	region := uiRegion(t, "scanner")
	requireUIContains(t, region,
		`id="panel-scanner"`, `id="scanTarget"`, `id="scanFilter"`, `id="scanRun"`, `id="scanClear"`,
		`id="scanRescanState"`, `id="scanCount"`, `id="checksBtn"`, `id="codecsBtn"`,
		`id="oobBtn" data-oob-ui aria-label="OOB">OOB</button>`,
		`id="scanPassiveView"`, `id="scanList" class="scan-list" role="listbox"`, `id="scanDetail"`,
		// workbench additions
		`id="scanToolsBtn"`, `aria-haspopup="true"`, `aria-expanded="false"`, `aria-controls="scanToolsMenu"`,
		`id="scanToolsMenu" role="group" aria-label="Scanner tools" hidden`,
		`id="scanDecoderBtn"`,
		`id="scanScopeLine"`, `id="scanScopeText"`, `id="scanScopeLink"`,
		`id="scanRunReason"`, `aria-describedby="scanRunReason"`,
		`id="scanProgress"`, `<progress id="scanProgressBar" aria-label="Scan progress">`, `id="scanProgressText" role="status"`,
		`id="scanSevChips" class="scan-sev-chips" role="group" aria-label="Filter results by severity"`,
		`class="scan-passive-view split"`, `class="split-list scan-list-col"`, `split-detail"`,
	)
	if strings.Contains(region, "style=") {
		t.Error("the scanner region must not use inline style attributes")
	}
	// Checks, Codecs, Decoder and OOB live behind the Tools menu, not in the bar.
	menu := region[strings.Index(region, `id="scanToolsMenu"`):]
	menu = menu[:strings.Index(menu, `id="scanScopeLine"`)]
	for _, id := range []string{"checksBtn", "codecsBtn", "scanDecoderBtn", "oobBtn"} {
		if !strings.Contains(menu, `id="`+id+`"`) {
			t.Errorf("%s must sit inside the Tools menu", id)
		}
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
	// The server exposes no out-of-scope counter, so none may be fabricated.
	if regexp.MustCompile(`(?i)auto-?forwarded:\s*\$|outOfScopeCount|autoForwarded`).MatchString(src + model) {
		t.Error("an out-of-scope auto-forward counter would be invented: the API exposes none")
	}
}

func TestUIInterceptForwardAllAndForwardToRepeaterReuseExistingEndpoints(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"forwardAllPlan(heldItems(),{dropping:dropMarks,rawCache:heldRawCache,originalCache:heldOriginalCache})",
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

func TestUIScannerCandidatesPromoteVerifyAndFilter(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	requireUIContains(t, src,
		"import { createSplitPane } from './split.js'",
		"import { renderState } from './statepanel.js'",
		"const body=promoteBody(g);",
		"api('/api/findings',{method:'POST'",
		"import('./tools.js').then(m=>m.sendToRepeater(target))",
		"import('./evidence-attach.js').then(m=>m.attachEvidence({kind:'flow',refs:[target.id]}",
		"function sevChip(sev)",
		"severityCounts(groups)",
		"filterGroups(all,scanUI.sevActive)",
		"renderState(list,'empty-filtered'",
		"scanUI.pane.showDetail(row)",
		"const back=host.querySelector(':scope > .split-back');",
		"openSettings('scope')",
	)
	// The new rendering code must not write inline styles.
	start := strings.Index(src, "function setScanDetailHTML(html)")
	end := strings.Index(src, "function setPromoteFindingState(button,stateName)")
	if start < 0 || end < start {
		t.Fatal("scanner rendering block not found")
	}
	if strings.Contains(src[start:end], `style="`) {
		t.Error("scanner result rendering must use classes, not inline style attributes")
	}
	// Severity is icon + text, never colour alone.
	model := readUIAsset(t, "js/scanner-model.js")
	requireUIContains(t, model, "Critical: 'alert-tri'", "Info: 'ring'")
}

func TestUIScannerRunGateExplainsItselfAndNeverTreatsEmptyScopeAsBlocked(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	requireUIContains(t, src,
		"scanRunGate().disabled",
		"reason.textContent=gate.reason",
		"scanUI.hostCount=hosts.length;scanUI.hostsLoaded=true;renderScanContext();",
		// Existing clear/run ownership contract is preserved verbatim.
		"clear.disabled=scanRunPending||scanClearPending",
		"setScanRunState('idle','Run scan ▸')",
		"if(scanRunPending||scanClearPending){scanResultsRefreshPending=true;return;}",
	)
	model := readUIAsset(t, "js/scanner-model.js")
	// An empty scope is "everything in scope" on the server; only missing traffic gates Run.
	requireUIContains(t, model, "hostsLoaded && Number(hostCount) === 0", "no include rule set, so all")
	if strings.Contains(src, "/api/scanner/stop") {
		t.Error("there is no scan-stop endpoint: do not wire a Stop button that cannot stop the scan")
	}
}

func TestUIScannerToolsMenuIsAnAccessibleDisclosure(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/scanner.js"))
	requireUIContains(t, src,
		"btn.setAttribute('aria-expanded',open?'true':'false')",
		"menu.addEventListener('click',()=>close(true),true);",
		"e.key==='Escape'&&!menu.hidden",
		"btn.focus({preventScroll:true})",
		"$('#scanDecoderBtn')?.addEventListener('click',()=>openDecoder())",
	)
	// Existing modal bindings still resolve to the same ids.
	requireUIContains(t, src, "if($('#checksBtn'))$('#checksBtn').onclick=openChecks;", "$('#oobBtn')&&($('#oobBtn').onclick=()=>{")
	requireUIContains(t, readUIAsset(t, "js/codecs.js"), "$('#codecsBtn').onclick = openCodecs")
}

func TestUIInterceptScannerStylesheetContract(t *testing.T) {
	css := readUIAsset(t, "panel-scan.css")
	if len(strings.TrimSpace(css)) == 0 {
		t.Fatal("panel-scan.css is empty")
	}
	requireUIContains(t, readUIAsset(t, "index.html"), `<link rel="stylesheet" href="/panel-scan.css">`)
	// No animation in this file: nothing to cover in the reduced-motion block.
	if regexp.MustCompile(`(?i)\btransition\s*:|@keyframes|\banimation\s*:`).MatchString(css) {
		t.Error("panel-scan.css must not animate; state is carried by text, icon and border style")
	}
	if strings.Contains(css, "@import") || strings.Contains(css, "url(http") {
		t.Error("panel-scan.css must be self-contained")
	}
	// Every custom property used must be defined in the shipped stylesheets.
	defined := map[string]bool{}
	for _, name := range []string{"app.css", "workbench.css", "primitives.css", "shell.css", "flow.css", "mobile.css", "surfaces.css"} {
		for _, m := range regexp.MustCompile(`(--[A-Za-z0-9-]+)\s*:`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			defined[m[1]] = true
		}
	}
	for _, m := range regexp.MustCompile(`var\((--[A-Za-z0-9-]+)`).FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] {
			t.Errorf("panel-scan.css uses undefined custom property %s", m[1])
		}
	}
	requireUIContains(t, css,
		".icpt-actionbar{", "position:sticky;bottom:0", ".scan-tools-menu[hidden]{display:none}",
		".scan-sev-chip[aria-pressed=\"true\"]", ".scan-passive-view[data-mode=\"stack\"] .scan-list{max-height:none}",
		"#panel-scanner>.toolbar{overflow:visible}", "@media (max-width:720px)", "@media (pointer:coarse)",
	)
	// Focus is visible on every new interactive.
	for _, sel := range []string{".icpt-actionbar .btn:focus-visible", "#scanToolsBtn:focus-visible", ".scan-sev-chip:focus-visible", ".scan-tgt-actions .btn:focus-visible"} {
		if !strings.Contains(css, sel) {
			t.Errorf("panel-scan.css lacks a visible focus ring for %s", sel)
		}
	}
	// No fixed min-width wider than the 375px phone minimum.
	for _, m := range regexp.MustCompile(`min-width:\s*(\d+)px`).FindAllStringSubmatch(css, -1) {
		if px, _ := strconv.Atoi(m[1]); px > 375 {
			t.Errorf("panel-scan.css has min-width %spx, wider than 375px", m[1])
		}
	}
}
