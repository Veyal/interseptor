package control

import (
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The desktop layouts of History and Intercept (docs/desktop-design.md, Panels)
// live in their own stylesheets and modules. These contracts keep them desktop
// only, inside the design system, and lossless: every web control is still
// there, moved but never removed.

var histIcptSheets = []string{"css/desktop/panel-intercept.css", "css/desktop/panel-proxy.css"}

func TestUIDesktopHistIcptPanelSheetsLoadAfterTheShellSheet(t *testing.T) {
	index := readUIAsset(t, "index.html")
	at := strings.Index(index, `<link rel="stylesheet" href="/css/desktop/shell.css">`)
	if at < 0 {
		t.Fatal("index.html lost shell.css")
	}
	for _, name := range histIcptSheets {
		link := `<link rel="stylesheet" href="/` + name + `">`
		i := strings.Index(index, link)
		if i < 0 {
			t.Fatalf("index.html does not link %s", name)
		}
		if i < at {
			t.Errorf("%s is linked before shell.css or out of alphabetical order", name)
		}
		at = i
		rec := uiGet(t, "/"+name, map[string]string{"Accept-Encoding": "gzip"})
		if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("/%s: status %d ETag %q encoding %q", name, rec.Code, rec.Header().Get("ETag"), rec.Header().Get("Content-Encoding"))
		}
	}
}

func TestUIDesktopHistIcptPanelSheetsFollowTheDesignSystem(t *testing.T) {
	ruleRE := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	for _, name := range histIcptSheets {
		css := stripCSSComments(readUIAsset(t, name))
		if len(ruleRE.FindAllString(css, -1)) < 10 {
			t.Fatalf("%s has too few rules", name)
		}
		for _, r := range ruleRE.FindAllStringSubmatch(css, -1) {
			for _, sel := range splitTopLevelCommas(r[1]) {
				if sel = strings.TrimSpace(sel); !strings.HasPrefix(sel, "html.desktop-app") {
					t.Errorf("%s: selector %q is not scoped under html.desktop-app; a browser tab would change", name, sel)
				}
			}
		}
		// Colours come from tokens; ids inside selectors (#panel-proxy) are not colours.
		for _, r := range ruleRE.FindAllStringSubmatch(css, -1) {
			body := r[0][strings.Index(r[0], "{"):]
			for _, m := range hex.FindAllString(body, -1) {
				t.Errorf("%s: literal colour %s; use a token", name, m)
			}
		}
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
			for _, part := range strings.Split(m[1], ",") {
				if f := strings.Fields(part); len(f) > 0 && f[0] != "transform" && f[0] != "opacity" && f[0] != "none" {
					t.Errorf("%s transitions %q; only transform and opacity may animate", name, f[0])
				}
			}
		}
		if strings.Contains(css, "@keyframes") || strings.Contains(css, "@import") || strings.Contains(css, "url(http") {
			t.Errorf("%s declares keyframes or loads an external resource", name)
		}
		for _, m := range regexp.MustCompile(`font-weight\s*:\s*(\d+)`).FindAllStringSubmatch(css, -1) {
			if m[1] != "400" && m[1] != "500" && m[1] != "600" {
				t.Errorf("%s uses font-weight %s; the desktop skin allows 400, 500 and 600", name, m[1])
			}
		}
		// Sentence case only; ::first-letter capitalisation is how a lowercased tag keeps its capital.
		for _, r := range ruleRE.FindAllStringSubmatch(css, -1) {
			if regexp.MustCompile(`text-transform\s*:\s*uppercase`).MatchString(r[0]) && !strings.Contains(r[1], "::first-letter") {
				t.Errorf("%s uppercases text for %q; the desktop skin is sentence case", name, strings.TrimSpace(r[1]))
			}
		}
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
			if regexp.MustCompile(`outline\s*:\s*(none|0)\b`).MatchString(m[2]) && !regexp.MustCompile(`(box-shadow|border(-color)?|background)\s*:`).MatchString(m[2]) {
				t.Errorf("%s removes the outline for %q without a replacement", name, strings.TrimSpace(m[1]))
			}
		}
	}
}

var histIcptModules = []string{"js/desktop-panel-proxy.js", "js/desktop-panel-intercept.js"}

func TestUIDesktopHistIcptPanelModulesRunOnlyInADesktopHost(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, name := range histIcptModules {
		if !strings.Contains(index, `<script type="module" src="/`+name+`"></script>`) {
			t.Errorf("index.html does not load %s", name)
		}
		js := readUIAsset(t, name)
		// All DOM work sits behind whenDesktop, which never fires without html.desktop-app.
		requireUIContains(t, js, "whenDesktop(")
		// No top-level statement reaches for the document; the helpers are functions.
		if m := regexp.MustCompile(`(?m)^(document|window|\$)\b`).FindString(js); m != "" {
			t.Errorf("%s has a top-level %q statement; DOM work belongs inside whenDesktop", name, m)
		}
		for _, m := range staticImportRE.FindAllStringSubmatch(js, -1) {
			switch m[1] {
			case "core.js", "desktop-panel-kit.js", "desktop-panel-proxy-model.js", "desktop-panel-intercept-model.js":
			default:
				t.Errorf("%s imports %s; a panel module may import core.js, the desktop kit and its model only", name, m[1])
			}
		}
		// Controls are moved, never deleted: the web handlers and ids must survive.
		for _, banned := range []string{".remove()", ".replaceWith(", "outerHTML", "removeChild("} {
			if strings.Contains(js, banned) {
				t.Errorf("%s uses %s; desktop layouts move controls and must not delete them", name, banned)
			}
		}
	}
	// One helper module: the kit owns the host check and the desktop gate.
	kit := readUIAsset(t, "js/desktop-panel-kit.js")
	requireUIContains(t, kit, "classList.contains('desktop-app')", "export function whenDesktop(")
	if _, err := uiFS.Open("ui/js/desktop-panel-util.js"); err == nil {
		t.Error("desktop-panel-util.js is folded into desktop-panel-kit.js; keep one helper module")
	}
}

func TestUIDesktopProxyLayoutKeepsEveryControl(t *testing.T) {
	index := readUIAsset(t, "index.html")
	// Markup the desktop layout rearranges stays in the web DOM.
	for _, id := range []string{
		"fSearchScope", "fSearch", "fMethod", "fStatus", "scopeToggle", "scopeEdit", "filtersBtn", "viewsBtn", "freezeBtn", "colPickerBtn",
		"proxyFilters", "activeFilterBar", "chips", "tlsDiagBanner", "inspectAddFinding", "inspectSendRepeater", "inspectSendIntruder",
		"inspectCopyPng", "inspectCopyPngMore", "inspectCopyAs", "inspectMoreActions", "noteBar", "noteInput", "noteSaved", "noteRetry",
	} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
	for _, view := range []string{"raw", "pretty", "headers", "json", "params", "gql", "decoded", "hex", "render"} {
		if !strings.Contains(index, `data-view="`+view+`"`) {
			t.Errorf("index.html lost the %s view button", view)
		}
	}
	js := readUIAsset(t, "js/desktop-panel-proxy.js")
	requireUIContains(t, js,
		// Filters group: scope controls moved in, not copied.
		"for (const id of ['scopeToggle', 'scopeEdit'])",
		// Method and Status stay the same native selects behind their pills.
		"[['fMethod', 'Method'], ['fStatus', 'Status']]",
		// Toolbar and inspector menus reach every moved control.
		"label: 'Columns…'", "label: 'Clear all filters'",
		"'Attach as evidence'", "'Copy as…'", "'Copy as PNG'", "'Copy as PNG options…'", "'More flow actions…'",
		// Views beyond Pretty, Raw and Hex are one menu away.
		"label: b.textContent.trim()",
	)
	css := stripCSSComments(readUIAsset(t, "css/desktop/panel-proxy.css"))
	requireUIContains(t, css,
		// One 40px toolbar row that never wraps, and no separate chip row.
		"#panel-proxy .proxy-toolbar{flex-wrap:nowrap", "height:var(--toolbar-h)", "#panel-proxy #activeFilterBar{display:none!important}",
		// One header row per inspector side.
		"#panel-proxy #inspect .pane-head{flex-wrap:nowrap",
		// The note is a compact affordance until used.
		"#noteInput:placeholder-shown",
	)
}

func TestUIDesktopInterceptLayoutKeepsEveryControl(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, id := range []string{
		"interceptWarning", "interceptToggle", "respInterceptToggle", "interceptFilterOn", "interceptFilterTarget", "interceptFilterPattern",
		"heldList", "heldTotal", "heldAutoFwd", "heldEditor", "heldViewSeg", "heldBeautifyBtn", "heldResetBtn", "heldDecodeBtn",
		"heldCtx", "heldScope", "heldScopeNote", "forwardBtn", "forwardRepeaterBtn", "dropBtn", "forwardAllBtn", "rulesBody", "addRuleBtn",
	} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
	js := readUIAsset(t, "js/desktop-panel-intercept.js")
	requireUIContains(t, js,
		// Beautify and Reset are one menu away; their buttons are stashed, not removed.
		"move(b, stash)", "label: 'Beautify body'", "label: 'Reset to original'",
		// The action row is ordered by the pure model.
		"orderActions(", "sameOrder(",
	)
	css := stripCSSComments(readUIAsset(t, "css/desktop/panel-intercept.css"))
	requireUIContains(t, css,
		// The amber banner duplicates the status pill and status bar.
		"#panel-intercept #interceptWarning:not(.is-unavailable){display:none!important}",
		// Forward is the one primary; Drop is a danger ghost; shortcut hints are generated.
		".icpt-actionbar .btn-danger{background:transparent", `content:"F" / ""`, `content:"D" / ""`,
	)
}

func TestUIDesktopHistIcptPanelModelsUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/desktop-panels-proxy-intercept.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}
