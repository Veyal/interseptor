package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP5 (UI overhaul): the Flow Drawer, the shared flow body renderer and the
// attachEvidence entry point. Pure logic runs under node; DOM-shaped behaviour is
// pinned statically against the embedded assets, as for the other ui_* tests.

func flowDrawerRegion(t *testing.T) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:flowdrawer -->")
	end := strings.Index(index, "<!-- /region:flowdrawer -->")
	if start < 0 || end < start {
		t.Fatal("region:flowdrawer markers missing")
	}
	return index[start:end]
}

func TestUIFlowDrawerPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/flowbody.test.mjs", "_js-tests/evidence-attach.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("flow drawer node tests failed: %v\n%s", err, out)
	}
}

// The pure modules must stay loadable under node, so neither may import core.js.
func TestUIFlowDrawerPureModulesDoNotImportCore(t *testing.T) {
	for _, name := range []string{"js/flowbody.js", "js/evidence-attach.js"} {
		if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(readUIAsset(t, name)) {
			t.Errorf("%s statically imports core.js; load it lazily so the module runs under node", name)
		}
	}
	if regexp.MustCompile(`(?m)^import `).MatchString(readUIAsset(t, "js/flowbody.js")) {
		t.Error("flowbody.js must have no imports")
	}
}

func TestUIFlowDrawerModulesNeverWriteInlineStyle(t *testing.T) {
	for _, name := range []string{"js/flowbody.js", "js/flowdrawer.js", "js/evidence-attach.js"} {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "style='") || strings.Contains(src, "cssText") {
			t.Errorf("%s writes inline style markup; use classes and custom properties", name)
		}
	}
	if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(executableJS(readUIAsset(t, "js/evidence-attach.js"))) {
		t.Error("evidence-attach.js must build nodes with textContent, never HTML strings")
	}
}

// Flow data is untrusted: the drawer may only assign HTML produced by the shared
// renderer (which escapes every value) or statepanel output.
func TestUIFlowDrawerOnlyAssignsRenderedBodyHTML(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/flowdrawer.js"))
	assigns := regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=[^;]*;|insertAdjacentHTML`).FindAllString(src, -1)
	if len(assigns) != 1 || !strings.Contains(assigns[0], "renderFlowBody(") {
		t.Errorf("flowdrawer.js HTML assignments must be exactly one renderFlowBody call, got %v", assigns)
	}
	body := executableJS(readUIAsset(t, "js/flowbody.js"))
	for _, want := range []string{"export const escapeHTML", "MAX_RENDER_BYTES = 1024 * 1024", "Truncated at 1 MB", "not rendered"} {
		if !strings.Contains(body, want) {
			t.Errorf("flowbody.js missing %q", want)
		}
	}
}

func TestUIFlowDrawerMarkupAndARIA(t *testing.T) {
	region := flowDrawerRegion(t)
	requireUIContains(t, region,
		`id="flowDrawer"`, `role="complementary"`, `aria-labelledby="fdTitle"`,
		`id="fdSash" role="separator" aria-orientation="vertical"`, `aria-valuemin="320"`, `aria-valuemax="640"`, `tabindex="0"`,
		`id="fdTabs" role="tablist"`, `id="fdBody" role="tabpanel"`,
		`id="fdAttach"`, `Attach as evidence`, `aria-keyshortcuts="e"`,
		`id="fdAttachMore"`, `aria-haspopup="menu"`,
		`id="fdRepeater"`, `id="fdIntruder"`, `id="fdScanner"`, `id="fdMore"`,
		`id="fdClose" aria-label="Close flow drawer"`, `id="fdStatus" role="status"`,
	)
	if strings.Contains(region, "style=") {
		t.Error("the drawer markup must not use inline styles")
	}
	if !regexp.MustCompile(`id="fdTitle"[^>]*tabindex="-1"`).MatchString(region) {
		t.Error("the drawer heading must be programmatically focusable so focus moves to it on open")
	}
}

func TestUIFlowDrawerFocusAndTrapContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/flowdrawer.js"))
	requireUIContains(t, src,
		"const OVERLAY_QUERY = '(max-width: 1100px)'",
		"panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-modal', 'true')",
		"panel.setAttribute('role', 'complementary'); panel.removeAttribute('aria-modal')",
		"cur.opener = opts.opener || document.activeElement",
		"$('#fdTitle').focus({ preventScroll: true })",
		"cur.opener.focus({ preventScroll: true })",
		"if (e.key === 'Escape' && !cur.overlay && !e.defaultPrevented)",
		"registerHook('openFlow', openFlow)",
		"registerHook('renderFlowBody'",
		"readSingleKeyPref()",
	)
	// The focus trap is core's modal stack and must only engage when overlaying.
	if n := strings.Count(src, "openModal(root"); n != 1 {
		t.Fatalf("openModal(root) must be called exactly once, got %d", n)
	}
	if !strings.Contains(src, "if (cur.overlay) {\n    openModal(root") {
		t.Error("the focus trap (openModal) must be guarded by cur.overlay so a docked drawer never traps focus")
	}
	if strings.Contains(src, "WP6") {
		t.Error("no work-package placeholders in shipped code")
	}
}

// The drawer fetches nothing until a flow is opened: every api() call sits inside
// a function body, never at module top level.
func TestUIFlowDrawerFetchesNothingAtLoad(t *testing.T) {
	for _, name := range []string{"js/flowdrawer.js", "js/evidence-attach.js"} {
		src := executableJS(readUIAsset(t, name))
		if loc := regexp.MustCompile(`(?m)^[^\s}/].*\bapi\(`).FindString(src); loc != "" {
			t.Errorf("%s calls api() at module top level: %q", name, loc)
		}
	}
	src := executableJS(readUIAsset(t, "js/flowdrawer.js"))
	// Raw text is never requested for binary or oversized bodies.
	requireUIContains(t, src, "rawState(cur.detail, side, bodyDeps) !== 'text'")
}

func TestUIEvidenceAttachUsesExistingEndpointsAndRefreshes(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/evidence-attach.js"))
	paths := regexp.MustCompile(`'/api/[^']*'`).FindAllString(src, -1)
	for _, p := range paths {
		if !strings.HasPrefix(p, "'/api/findings") {
			t.Errorf("evidence-attach.js calls %s; only existing /api/findings endpoints are allowed", p)
		}
	}
	requireUIContains(t, src,
		"'/flows'", "'/images'", "'/flows/' + id",
		"refresh({ reason }",
		"export const UNDO_MS = 5000",
		"New finding from selection",
		"role', 'combobox'", "role', 'listbox'", "role', 'option'", "aria-activedescendant",
		"aria-expanded",
		"validateAltText", "alt text is required",
	)
	// Attaching must go through projectState.refresh after the mutation.
	if strings.Index(src, "await refreshState('attach')") < 0 {
		t.Error("attach must refresh projectState after mutating")
	}
}

func TestUIEvidenceAttachKeyboardAndDrag(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/evidence-attach.js"))
	if n := strings.Count(src, "registry.register("); n != 2 {
		t.Errorf("the e key must be registered through the keys.js registry in two scopes, got %d registrations", n)
	}
	requireUIContains(t, src,
		"keys: 'e', scope: 'proxy-list'", "keys: 'e', scope: 'flow-drawer'",
		"createKeyRegistry({ isModalOpen: () => core.hasOpenModal() })",
	)
	if strings.Contains(src, "e.key === 'e'") || strings.Contains(src, `e.key === "e"`) {
		t.Error("the e binding must go through the registry (typing/modal/switch gating), not a raw key check")
	}
	if n := strings.Count(src, "addEventListener('dragstart'"); n != 1 {
		t.Errorf("exactly one delegated dragstart listener is allowed, got %d", n)
	}
	requireUIContains(t, src, "data-evidence-drop", "FLOW_DRAG_TYPE")
}

func TestUIFlowModalIsAShimOverTheDrawer(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/flowmodal.js"))
	requireUIContains(t, src,
		"export function closeFlowPopup(", "export async function flowPopup(", "export async function fmRenderSide(",
		"if(openFlow(id,{source:'popup'}))return;",
		"getHook('closeFlow')",
	)
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, "'flowDrawer'") {
		t.Error("flowDrawer must stay in MODAL_IDS so overlay mode is treated as a modal")
	}
	if !strings.Contains(readUIAsset(t, "js/shell-hooks.js"), "'flowbody', 'flowdrawer', 'evidence-attach'") {
		t.Error("the three flow drawer modules must stay in OPTIONAL_MODULES")
	}
}

func TestUIFlowDrawerStylesAreResponsiveAndMotionBounded(t *testing.T) {
	css := readUIAsset(t, "flow.css")
	requireUIContains(t, css,
		".flow-drawer[data-mode=\"overlay\"]", "@media (max-width:720px)", "@media (pointer:coarse)",
		"var(--sticky-top)", "var(--z-drawer)", "var(--hit-min)", "var(--safe-b)",
	)
	for _, m := range regexp.MustCompile(`@keyframes [\w-]+\{([^@]*?\})\}`).FindAllStringSubmatch(css, -1) {
		if regexp.MustCompile(`(width|height|left|top|margin|padding|color|background)\s*:`).MatchString(m[1]) {
			t.Errorf("keyframes may animate only transform and opacity: %s", m[1])
		}
	}
	if regexp.MustCompile(`font-size:\s*[0-9.]+px`).MatchString(css) {
		t.Error("flow.css must use the --fs-* scale, not px font sizes")
	}
	if regexp.MustCompile(`min-width:\s*[4-9][0-9]{2}px|min-width:\s*[0-9]{4}px`).MatchString(css) {
		t.Error("nothing may set a min-width wider than 375px")
	}
}
