package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP2 (UI overhaul shell): the Engagement Strip, Connection popover, project
// state store, palette extraction and the shell extension points. The UI has no
// build step, so pure logic runs under node and DOM-shaped behaviour is pinned
// statically against the embedded assets.

func shellRegion(t *testing.T) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:shell -->")
	end := strings.Index(index, "<!-- /region:shell -->")
	if start < 0 || end < start {
		t.Fatal("region:shell markers missing")
	}
	return index[start:end]
}

func TestUIShellPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/project-state.test.mjs", "_js-tests/shell-hooks.test.mjs", "_js-tests/connection.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell node tests failed: %v\n%s", err, out)
	}
}

// The pure modules must stay loadable under node, so none may import core.js.
func TestUIShellPureModulesDoNotImportCore(t *testing.T) {
	for _, name := range []string{"js/project-state.js", "js/ctxbar.js", "js/connection.js", "js/shell-hooks.js"} {
		if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(readUIAsset(t, name)) {
			t.Errorf("%s statically imports core.js; take dependencies through init functions", name)
		}
	}
}

func TestUIShellModulesNeverWriteInlineStyleOrUnsafeHTML(t *testing.T) {
	for _, name := range []string{"js/project-state.js", "js/ctxbar.js", "js/connection.js", "js/shell-hooks.js"} {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "style='") || strings.Contains(src, "cssText") {
			t.Errorf("%s writes inline style markup; use classes and custom properties", name)
		}
		if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
			t.Errorf("%s assigns HTML strings; build nodes with textContent", name)
		}
	}
}

func TestUIShellPreservesRequiredIDs(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, want := range []string{
		`id="tabs"`, `data-tab="proxy"`, `data-tab="intercept"`, `data-tab="repeater"`, `data-tab="intruder"`,
		`data-tab="scanner"`, `data-tab="map"`, `data-tab="findings"`, `data-tab="notes"`, `data-tab="activity"`, `data-tab="settings"`,
		`id="crumb"`, `id="mobileToolSelect"`, `id="sseStatus"`, `id="proxyAddr"`, `id="controlAddr"`,
		`id="deviceProxyChip"`, `id="cmdkBtn"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html lost %s", want)
		}
	}
	// findReadinessBoard lives in the findings region (WP7 replaces its renderer).
	if !strings.Contains(index, `id="findReadinessBoard"`) {
		t.Error("index.html lost #findReadinessBoard")
	}
	if !regexp.MustCompile(`id="crumb" class="sr-only"`).MatchString(index) {
		t.Error("#crumb must stay in the DOM as a visually hidden element")
	}
	if !regexp.MustCompile(`id="sseStatus" role="status" aria-live="polite"`).MatchString(index) {
		t.Error("#sseStatus must keep role=status")
	}
}

func TestUIShellEngagementStripSemantics(t *testing.T) {
	shell := shellRegion(t)
	requireUIContains(t, shell,
		`id="ctxbar" role="region" aria-label="Engagement"`,
		// Scope chip: real button with switch semantics.
		`id="ctxScope" class="ctx-chip ctx-scope" role="switch" aria-checked="false"`,
		// Readiness meter: role, range and value text.
		`id="ctxMeter" class="ctx-meter" role="meter"`, `aria-valuemin="0"`, `aria-valuemax="1"`, `aria-valuenow="0"`, `aria-valuetext="No findings yet"`,
		// Blockers popover trigger and non-modal popover.
		`id="ctxBlockers" class="ctx-chip ctx-blockers" aria-haspopup="dialog" aria-expanded="false" aria-controls="ctxBlockerPop"`,
		`id="ctxBlockerPop" class="ctx-pop" role="dialog" aria-modal="false" aria-labelledby="ctxBlockerTitle" hidden`,
		`id="ctxIdentity"`, `id="ctxIdentityWrap" class="ctx-identity" hidden`,
		`id="ctxTarget"`, `id="ctxEvidence"`, `id="ctxNext"`,
	)
	// Every chip is a real <button>.
	for _, id := range []string{"ctxProject", "ctxScope", "ctxTarget", "ctxEvidence", "ctxBlockers", "ctxNext", "ctxMore"} {
		if !regexp.MustCompile(`<button type="button" id="` + id + `"`).MatchString(shell) {
			t.Errorf("#%s must be a <button type=button>", id)
		}
	}
	// One polite live region for the blocker count only.
	if n := strings.Count(shell, `id="ctxLive"`); n != 1 {
		t.Errorf("ctxLive appears %d times, want exactly 1", n)
	}
	requireUIContains(t, shell, `id="ctxLive" class="u-sr" role="status" aria-live="polite"`)
	// No project data in static markup: locked/unauthenticated pages never carry it.
	strip := shell[strings.Index(shell, `id="ctxbar"`):]
	if strings.Contains(strip, "example.com") {
		t.Error("the strip markup must not embed a hostname")
	}
	if strings.Contains(shell, `aria-live="polite" title="Proxy listener"`) || strings.Contains(shell, `class="row u-gap-2" title="Proxy listener"`) {
		t.Error("the topbar must no longer carry the listener addresses")
	}
}

func TestUIShellConnectionPopoverHoldsPreservedIDs(t *testing.T) {
	shell := shellRegion(t)
	pop := shell[strings.Index(shell, `id="connPop"`):strings.Index(shell, `id="themeToggle"`)]
	for _, id := range []string{"sseStatus", "sseDot", "sseLabel", "sseRetry", "proxyAddr", "deviceProxyChip", "deviceProxyAddr", "controlAddr", "capDot", "capStat"} {
		if !strings.Contains(pop, `id="`+id+`"`) {
			t.Errorf("connection popover must hold #%s", id)
		}
	}
	requireUIContains(t, shell,
		`id="connChip" class="conn-chip"`, `aria-haspopup="dialog" aria-expanded="false" aria-controls="connPop"`,
		`id="offlineBanner" class="offline-banner" role="status" hidden`,
	)
	conn := executableJS(readUIAsset(t, "js/connection.js"))
	requireUIContains(t, conn,
		"export const OFFLINE_BANNER_MS = 5000",
		"e.key !== 'Escape'", "restoreFocus", "aria-expanded",
		"wrap.setAttribute('aria-label'", "wrap.classList.toggle('reconnecting'",
	)
}

func TestUIShellProjectStateContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/project-state.js"))
	requireUIContains(t, src,
		"export const REFRESH_DEBOUNCE_MS = 250", "export const FLOW_NEW_THROTTLE_MS = 2000", "export const ITEMS_CAP = 200",
		"Promise.allSettled", "'/api/project/readiness'", "'/api/authz'", "'/api/scope'", "'/api/engagement-brief'", "'/api/findings'",
		"export function createProjectState", "export const projectState",
		"subscribe(fn)", "refresh()", "noteFlowNew()",
	)
	// The client displays readiness; it must not recompute pass/fail from checks.
	for _, banned := range []string{".checks.filter", "check.pass", "checks.every", "checks.some", ".passed"} {
		if strings.Contains(src, banned) {
			t.Errorf("project-state.js re-derives readiness (%q); display server gaps only", banned)
		}
	}
}

func TestUIShellScopeToggleReusesExistingFilter(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "toggleScopeFilter:()=>$('#scopeToggle')?.click()", "scopeFilterOn:()=>!!state.inScopeOnly")
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	if strings.Contains(ctx, "'/api/scope'") || strings.Contains(ctx, "method: 'POST'") || strings.Contains(ctx, "method: 'PUT'") {
		t.Error("the strip must not mutate scope rules; it mirrors the History in-scope filter")
	}
	requireUIContains(t, ctx, "e.shiftKey", "aria-disabled")
}

func TestUIShellAppExposesExtensionPointsAndKeepsOriginalSSEEvents(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"export { registerCommand, registerSseHandler }",
		"emitTabChange(t.dataset.tab,prev?prev.dataset.panel:'')",
		"runSseHooks(m)", "dispatchSseMessage(m)", "loadOptionalModules(path=>import(path))",
		"function refreshProjectState(reason){if(projectScopedUIReady)projectState.refresh({reason});}",
		"projectState.attach({fetchJson:path=>api(path)})",
		"registerSseHandler('flow.new',()=>{if(projectScopedUIReady)projectState.noteFlowNew();})",
		"refreshProjectState('resync')",
	)
	// Every original event name is still handled by the original chain.
	for _, ev := range []string{
		"flow.new", "flow.update", "activity", "activity.clear", "intercept.update", "rules.update", "ws.frame",
		"scope.update", "views.update", "session.update", "settings.update", "human.input", "tunnel.update",
		"checks.update", "codecs.update", "oob.update", "intruder.update", "scanner.update", "notes.update",
		"engagement.update", "findings.update", "tags.update", "allowlist.update",
	} {
		if !strings.Contains(app, "'"+ev+"'") {
			t.Errorf("app.js no longer handles SSE event %s", ev)
		}
	}
	if !strings.Contains(app, "es.addEventListener('hello'") {
		t.Error("the hello handler must remain")
	}
	if strings.Count(app, "new EventSource(") != 1 {
		t.Error("exactly one EventSource must remain")
	}
}

func TestUIShellHooksListEveryOptionalModuleGuarded(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/shell-hooks.js"))
	for _, name := range []string{"flowbody", "flowdrawer", "evidence-attach", "evidence-tray", "readiness-meter", "report-preflight", "checklist", "dock", "repeater", "intruder", "proxy-filters", "proxy-selection"} {
		if !strings.Contains(src, "'"+name+"'") {
			t.Errorf("optional module %s is not in the guarded import list", name)
		}
	}
	requireUIContains(t, src, "try { await importer('./' + name + '.js')", "catch (e) { onError(e, name)")
}

func TestUIShellPaletteExtractionKeepsEveryCommand(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, fn := range []string{"function cmdkBuild()", "function cmdkRender()", "function cmdkPaint()", "function cmdkRun("} {
		if strings.Contains(app, fn) {
			t.Errorf("app.js still defines %s; it lives in cmdk.js now", fn)
		}
	}
	cmdk := executableJS(readUIAsset(t, "js/cmdk.js"))
	requireUIContains(t, cmdk, "function cmdkBuild()", "function cmdkRender()", "function cmdkPaint()", "function cmdkRun(", "export function cmdkOpen()", "export function cmdkClose()", "export function configureCommandPalette", "listCommands()")
	if strings.Contains(cmdk, "style=") || strings.Contains(cmdk, "cssText") {
		t.Error("the palette must use classes, not inline styles")
	}
	// The base list moved with no entry lost.
	for _, title := range []string{"Go to Proxy", "Go to Intercept", "Go to Repeater", "Go to Intruder", "Go to Scanner", "Go to Findings", "Go to Map", "Go to Notes", "Go to Activity",
		"New finding", "Export findings", "Switch or create project", "Run setup wizard", "Toggle theme (dark / light)", "Shortcuts", "Settings: Target scope"} {
		if !strings.Contains(app, "t:'"+title+"'") {
			t.Errorf("palette command %q is missing", title)
		}
	}
}

func TestUIShellThemeBootKeepsHighContrast(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"return t==='light'||t==='hc'?t:'dark'",
		"if(t==='light'||t==='hc')document.documentElement.setAttribute('data-theme',t)",
	)
}

func TestUIShellStylesheetHasPhoneAndNarrowRules(t *testing.T) {
	css := readUIAsset(t, "shell.css")
	body := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	for _, rule := range []string{
		"@media (max-width:900px){\n  .ctx-evidence{display:none}",
		"@media (max-width:720px){",
		".ctx-more{display:inline-flex}",
		"@media (max-width:480px){\n  .ctx-target .ctx-text{display:none}",
		"@media (pointer:coarse){",
		"@media (forced-colors:active){",
	} {
		if !strings.Contains(body, rule) {
			t.Errorf("shell.css missing %q", rule)
		}
	}
	phone := mediaBlock(t, body, "@media (max-width:720px)")
	for _, hidden := range []string{".ctx-project", ".ctx-identity", ".ctx-next"} {
		if !strings.Contains(phone, hidden) {
			t.Errorf("the 720px strip must collapse %s into the details sheet", hidden)
		}
	}
	// Side gutters of at least 16px, and nothing wider than a 375px phone.
	requireUIContains(t, body, "padding:var(--sp-2) max(var(--sp-4),var(--safe-r)) var(--sp-2) max(var(--sp-4),var(--safe-l))")
	for _, m := range regexp.MustCompile(`min-width\s*:\s*(\d+)px`).FindAllStringSubmatch(body, -1) {
		if m[1] != "44" && m[1] != "0" {
			t.Errorf("shell.css sets min-width:%spx; nothing may be wider than the 375px target", m[1])
		}
	}
	// Chips keep a focus ring and the 24px minimum target.
	requireUIContains(t, body, ".ctx-chip:focus-visible", "outline:2px solid var(--focus-ring)", "min-height:var(--hit-min)")
}

func TestUIShellStylesheetAnimatesNothingButCompositorProperties(t *testing.T) {
	body := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(readUIAsset(t, "shell.css"), "")
	for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(body, -1) {
		for _, part := range strings.Split(m[1], ",") {
			if f := strings.Fields(strings.TrimSpace(part)); len(f) > 0 && f[0] != "transform" && f[0] != "opacity" && f[0] != "none" {
				t.Errorf("shell.css transitions %q; only transform and opacity may animate", f[0])
			}
		}
	}
	if strings.Contains(body, "@keyframes") {
		t.Error("shell.css must not add keyframes; the strip conveys state without motion")
	}
}

func TestUIShellStripIsGatedUntilHydrated(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	// The store only fetches through refreshProjectState, which needs hydration.
	if strings.Count(app, "projectState.refresh(") != 1 {
		t.Error("projectState.refresh must be called from exactly one gated place")
	}
	requireUIContains(t, app, "function completeProjectScopedUIHydration(statuses){\n  projectScopedUIReady=true;\n  refreshProjectState('hydrated');")
	ctx := readUIAsset(t, "js/ctxbar.js")
	if strings.Contains(ctx, "fetch(") || strings.Contains(ctx, "api(") {
		t.Error("ctxbar.js must not fetch; it renders projectState")
	}
}

func TestUIShellBlockerPopoverContract(t *testing.T) {
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, ctx,
		"e.key !== 'Escape' || !popOpen", "deps.hasOpenModal && deps.hasOpenModal()",
		"closeBlockerPopover({ restoreFocus: true })", "chip.setAttribute('aria-expanded'",
		"const POPOVER_ROW_CAP = 50", "aria-valuetext", "readinessValuetext(",
		"createBlockerAnnouncer(", "aria-busy",
	)
	if strings.Contains(ctx, "aria-modal', 'true'") {
		t.Error("the blockers popover is non-modal")
	}
}

func TestUIShellRailBadgeAndCrumbIgnoreBadges(t *testing.T) {
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, ctx, "badge.id = 'findBadge'", "'visually-hidden', ' report blockers'", "badge.classList.toggle('u-hidden'")
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "label.querySelectorAll('.badge,.nav-dot,.nav-dot-note').forEach(n=>n.remove())")
	requireUIContains(t, readUIAsset(t, "shell.css"), "#tab-findings #findBadge")
}
