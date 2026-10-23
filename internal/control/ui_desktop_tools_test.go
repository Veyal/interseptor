package control

import (
	"io/fs"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The desktop skin of the test panels (Repeater, Intruder, Collections;
// docs/desktop-design.md, Panels). These contracts keep a browser tab on the web
// look (every rule is scoped under html.desktop-app and the JS starts only in a
// desktop host), keep every control reachable (controls move by re-parenting,
// never by removal or cloning), and keep the sheets inside the design system.

var desktopToolsSheets = []string{
	"css/desktop/panel-collections.css", "css/desktop/panel-identities.css", "css/desktop/panel-intruder.css",
	"css/desktop/panel-repeater.css", "css/desktop/panel-tools.css",
}

var desktopToolsModules = []string{
	"js/desktop-panel-tools.js", "js/desktop-panel-kit.js", "js/desktop-panel-tools-model.js",
	"js/desktop-panel-repeater.js", "js/desktop-panel-intruder.js", "js/desktop-panel-collections.js",
	"js/desktop-panel-identities.js",
}

// flattenCSSRules returns every "selector{body}" pair, descending into @media
// and @container blocks.
func flattenCSSRules(css string) [][2]string {
	var out [][2]string
	var walk func(s string)
	walk = func(s string) {
		for {
			open := strings.Index(s, "{")
			if open < 0 {
				return
			}
			head := strings.TrimSpace(s[:open])
			depth, end := 1, open+1
			for end < len(s) && depth > 0 {
				switch s[end] {
				case '{':
					depth++
				case '}':
					depth--
				}
				end++
			}
			body := s[open+1 : end-1]
			if strings.HasPrefix(head, "@media") || strings.HasPrefix(head, "@container") || strings.HasPrefix(head, "@supports") {
				walk(body)
			} else {
				out = append(out, [2]string{head, body})
			}
			s = s[end:]
		}
	}
	walk(css)
	return out
}

func TestUIDesktopToolsSheetsAreLinkedAfterTheSkinAndServed(t *testing.T) {
	index := readUIAsset(t, "index.html")
	at := strings.Index(index, `<link rel="stylesheet" href="/css/desktop/shell.css">`)
	if at < 0 {
		t.Fatal("index.html lost desktop/shell.css")
	}
	for _, name := range desktopToolsSheets {
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
	// One entry script, after app.js; the web markup never carries desktop DOM.
	app := strings.Index(index, `<script type="module" src="/js/app.js"></script>`)
	entry := strings.Index(index, `<script type="module" src="/js/desktop-panel-tools.js"></script>`)
	if app < 0 || entry < app {
		t.Error("desktop-panel-tools.js must be loaded once, after app.js")
	}
	for _, name := range desktopToolsModules {
		if rec := uiGet(t, "/"+name, nil); rec.Code != http.StatusOK {
			t.Errorf("/%s: status %d", name, rec.Code)
		}
	}
}

func TestUIDesktopToolsRulesAreScopedAndFollowTheDesignSystem(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	weights := regexp.MustCompile(`font-weight\s*:\s*(\d+)|font\s*:\s*(\d{3})\s`)
	for _, name := range desktopToolsSheets {
		raw := readUIAsset(t, name)
		css := stripCSSComments(raw)
		rules := flattenCSSRules(css)
		if len(rules) == 0 {
			t.Fatalf("%s has no rules", name)
		}
		for _, r := range rules {
			for _, sel := range splitTopLevelCommas(r[0]) {
				if sel = strings.TrimSpace(sel); !strings.HasPrefix(sel, "html.desktop-app") {
					t.Errorf("%s: selector %q is not scoped under html.desktop-app; a browser tab would change", name, sel)
				}
			}
			// A removed outline always comes with a visible replacement.
			if regexp.MustCompile(`outline\s*:\s*(none|0)\b`).MatchString(r[1]) && !regexp.MustCompile(`(box-shadow|border(-color)?|background)\s*:`).MatchString(r[1]) {
				t.Errorf("%s removes the outline for %q without a replacement", name, r[0])
			}
		}
		for _, m := range hex.FindAllString(css, -1) {
			t.Errorf("%s: literal colour %s; colours come from desktop/tokens.css", name, m)
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
		for _, m := range weights.FindAllStringSubmatch(css, -1) {
			if w := m[1] + m[2]; w != "400" && w != "500" && w != "600" {
				t.Errorf("%s uses font-weight %s; the desktop skin allows 400, 500 and 600", name, w)
			}
		}
		if strings.Contains(css, "text-transform:uppercase") || strings.Contains(css, "text-transform: uppercase") {
			t.Errorf("%s uppercases text; the desktop skin is sentence case", name)
		}
	}
}

// Panel files use #panel ids so they outrank the shared class selectors in
// panel-tools.css whatever the order of the links; the shared file must not
// reach for panel ids itself.
func TestUIDesktopToolsSharedSheetUsesClassSelectors(t *testing.T) {
	for _, r := range flattenCSSRules(stripCSSComments(readUIAsset(t, "css/desktop/panel-tools.css"))) {
		if strings.Contains(r[0], "#panel-") {
			t.Errorf("panel-tools.css selector %q uses a panel id; per-panel rules belong in the panel's sheet", r[0])
		}
	}
	for _, name := range []string{"css/desktop/panel-repeater.css", "css/desktop/panel-intruder.css", "css/desktop/panel-collections.css", "css/desktop/panel-identities.css"} {
		panel := strings.TrimSuffix(strings.TrimPrefix(name, "css/desktop/panel-"), ".css")
		for _, r := range flattenCSSRules(stripCSSComments(readUIAsset(t, name))) {
			if !strings.Contains(r[0], "#panel-"+panel) {
				t.Errorf("%s selector %q does not name #panel-%s", name, r[0], panel)
			}
		}
	}
}

func TestUIDesktopToolsModulesAreLeavesGatedOnTheDesktopHost(t *testing.T) {
	allowed := map[string]bool{}
	for _, name := range desktopToolsModules {
		allowed[strings.TrimPrefix(name, "js/")] = true
	}
	for _, name := range desktopToolsModules {
		js := readUIAsset(t, name)
		for _, m := range staticImportRE.FindAllStringSubmatch(js, -1) {
			if !allowed[m[1]] {
				t.Errorf("%s imports %s; the desktop panel modules import only each other", name, m[1])
			}
		}
		// The web markup is never rewritten, and nothing is cloned or deleted: a
		// control moves by re-parenting, so its id, listeners and state survive.
		for _, bad := range []string{"cloneNode(", ".remove()", "?.remove()", "removeChild(", "outerHTML", "document.write"} {
			if strings.Contains(js, bad) {
				t.Errorf("%s uses %s; desktop panel code re-parents controls and never removes or clones them", name, bad)
			}
		}
	}
	entry := readUIAsset(t, "js/desktop-panel-tools.js")
	requireUIContains(t, entry,
		"if (!isDesktop() || boot.done) return;",
		"obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });",
	)
	kit := readUIAsset(t, "js/desktop-panel-kit.js")
	requireUIContains(t, kit, "document.documentElement.classList.contains('desktop-app')")
	// A closed menu stays measurable and focusable: popovers anchored to a moved
	// control need its rectangle, and focus returned to a moved control (a sheet
	// closing) must land somewhere real; the kit hands it on to the trigger.
	tools := readUIAsset(t, "css/desktop/panel-tools.css")
	requireUIContains(t, tools, `html.desktop-app .dsk-menu[data-open="false"]{opacity:0;pointer-events:none}`)
	if strings.Contains(stripCSSComments(tools), "visibility:hidden") {
		t.Error("a closed .dsk-menu must not be visibility:hidden: focus returned to a moved control would land on <body>")
	}
	requireUIContains(t, kit, "menu.addEventListener('focusin', () => { if (!isOpen()) button.focus({ preventScroll: true }); });")
}

// Every control the desktop skin moves into a menu or relabels must still be
// there: it is looked up by id, so the id has to exist in the web markup or be
// created by the web modules.
func TestUIDesktopToolsMovedControlsStillExist(t *testing.T) {
	index := readUIAsset(t, "index.html")
	web := index + readUIAsset(t, "js/tools.js") + readUIAsset(t, "js/repeater.js") + readUIAsset(t, "js/intruder.js") +
		readUIAsset(t, "js/collections-editor.js") + readUIAsset(t, "js/collections-response.js") + readUIAsset(t, "js/collections-core.js")
	idRE := regexp.MustCompile(`#([A-Za-z][A-Za-z0-9]*)`)
	var skip = map[string]bool{
		// Built by the desktop modules themselves.
		"repActionRow": true, "repMore": true, "repBarMore": true, "intrMore": true, "intrInfo": true, "collMoreTools": true,
		"intrRunLine": true, "identMoreTools": true,
	}
	for _, name := range []string{"js/desktop-panel-repeater.js", "js/desktop-panel-intruder.js", "js/desktop-panel-collections.js", "js/desktop-panel-identities.js"} {
		js := stripJSLineComments(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`\$\('([^']+)'\)`).FindAllStringSubmatch(js, -1) {
			for _, id := range idRE.FindAllStringSubmatch(m[1], -1) {
				if skip[id[1]] {
					continue
				}
				if !strings.Contains(web, id[1]) {
					t.Errorf("%s looks up #%s, which neither the web markup nor the web modules define", name, id[1])
				}
			}
		}
	}
	// The labels the desktop skin rewrites keep their accessible name source.
	for _, id := range []string{"repAttach", "repProof", "repDiffBtn", "repCopyAs", "repCopyPng", "repToIntruder", "repAddFinding", "repCopyCurl", "repPostmanImport",
		"intrAttach", "intrDiffBtn", "intrRender", "intrHistToggle", "intrToFinding", "collImport", "collExport", "collEnvEdit", "collEnvNew", "collEnvManage", "collConsoleBtn", "collRun", "collMore", "collNew"} {
		if !strings.Contains(web, id) {
			t.Errorf("the web modules lost #%s", id)
		}
	}
}

func stripJSLineComments(s string) string {
	return regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(s, "")
}

func TestUIDesktopToolsModelUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/desktop-panel-tools-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

// One helper module for the whole desktop skin: the host check, the platform
// check, the icon builder and popup dismissal (outside press, Escape) exist once, in desktop-panel-kit.js, and the
// Repeater Send hint has one owner.
func TestUIDesktopHelpersLiveOnceInTheKit(t *testing.T) {
	entries, err := fs.ReadDir(uiFS, "ui/js")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "desktop") || name == "desktop-panel-kit.js" || !strings.HasSuffix(name, ".js") {
			continue
		}
		js := stripJSLineComments(readUIAsset(t, "js/"+name))
		for _, dup := range []string{"function iconNode(", "classList.contains('desktop-app')", "/Mac|iPhone|iPad/", "document.addEventListener('pointerdown'", "e.key === 'Escape'"} {
			if name == "desktop.js" && dup == "/Mac|iPhone|iPad/" {
				continue // the bridge leaf detects the host itself and owns keyboard modality
			}
			if strings.Contains(js, dup) {
				t.Errorf("%s repeats %q; take it from desktop-panel-kit.js", name, dup)
			}
		}
	}
	if strings.Contains(readUIAsset(t, "js/desktop-shell.js"), "#repSend .kbd") {
		t.Error("desktop-shell.js rewrites the Repeater Send hint; desktop-panel-repeater.js owns it")
	}
	requireUIContains(t, readUIAsset(t, "js/desktop-panel-repeater.js"), "$('#repSend .kbd')")
}
