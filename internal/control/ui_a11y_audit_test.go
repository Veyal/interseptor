package control

import (
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// WP14 (UI overhaul): cross-cutting static audit. Each earlier package pins its
// own contracts; these tests sweep every embedded stylesheet, script and the
// shell markup so a later edit to any one of them cannot quietly regress the
// shared accessibility, motion and privacy rules (spec section 11).

var cssCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripCSSComments(s string) string { return cssCommentRE.ReplaceAllString(s, "") }

func uiScriptNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(uiFS, "ui/js")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".js") {
			names = append(names, "js/"+e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// --- Icons -----------------------------------------------------------------

// TestUIA11yNoGlyphIconsInScriptsOrMarkup extends the static-markup glyph ban
// to every script: controls draw the sprite (icon() or <use href="#i-...">) so
// they follow the theme and forced-colors. Disclosure triangles drawn by CSS
// `content:` are decorative pseudo-elements, not controls, and stay.
func TestUIA11yNoGlyphIconsInScriptsOrMarkup(t *testing.T) {
	glyphs := []string{"⧉", "◎", "▦", "＋", "◧", "▾", "✕"}
	assets := append([]string{"index.html", "login.html"}, uiScriptNames(t)...)
	for _, name := range assets {
		body := readUIAsset(t, name)
		for _, g := range glyphs {
			if strings.Contains(body, g) {
				t.Errorf("%s uses the Unicode glyph %s as an icon; use the sprite", name, g)
			}
		}
	}
}

func TestUIA11yEverySpriteReferenceResolves(t *testing.T) {
	index := readUIAsset(t, "index.html")
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`<symbol id="(i-[a-z0-9-]+)"`).FindAllStringSubmatch(index, -1) {
		defined[m[1]] = true
	}
	use := regexp.MustCompile(`href="#(i-[a-z0-9-]+)"`)
	call := regexp.MustCompile(`\bicon(?:Svg|Node|Wrap)?\(\s*'(?:i-)?([a-z0-9-]+)'`)
	for _, name := range append([]string{"index.html"}, uiScriptNames(t)...) {
		body := readUIAsset(t, name)
		for _, m := range use.FindAllStringSubmatch(body, -1) {
			if !defined[m[1]] {
				t.Errorf("%s references undefined sprite symbol %s", name, m[1])
			}
		}
		for _, m := range call.FindAllStringSubmatch(body, -1) {
			if !defined["i-"+m[1]] {
				t.Errorf("%s calls icon('%s') but the sprite has no i-%s", name, m[1], m[1])
			}
		}
	}
}

// --- Motion ----------------------------------------------------------------

// TestUIA11yEveryStylesheetAnimatesOnlyCompositorProperties sweeps every
// stylesheet the overhaul added or filled. app.css and surfaces.css predate the
// transform/opacity rule (colour and shadow transitions on legacy controls and
// menus); they are covered by the global reduced-motion block instead, and the
// sheet half of surfaces.css is pinned by TestUIPrimitiveStylesheetsAnimateOnlyCompositorProperties.
func TestUIA11yEveryStylesheetAnimatesOnlyCompositorProperties(t *testing.T) {
	for _, name := range newFoundationStylesheets {
		body := stripCSSComments(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(body, -1) {
			for _, part := range strings.Split(m[1], ",") {
				f := strings.Fields(strings.TrimSpace(part))
				if len(f) == 0 {
					continue
				}
				switch f[0] {
				case "transform", "opacity", "none", "var(--motion-none)":
				default:
					t.Errorf("%s transitions %q; only transform and opacity may animate", name, f[0])
				}
			}
		}
		for _, m := range regexp.MustCompile(`@keyframes\s+([\w-]+)\s*\{((?:[^{}]|\{[^{}]*\})*)\}`).FindAllStringSubmatch(body, -1) {
			for _, d := range regexp.MustCompile(`([a-z-]+)\s*:`).FindAllStringSubmatch(m[2], -1) {
				if d[1] != "transform" && d[1] != "opacity" {
					t.Errorf("%s @keyframes %s animates %q; only transform and opacity may animate", name, m[1], d[1])
				}
			}
		}
	}
}

func TestUIA11yReducedMotionBlockIsGlobalAndEveryAnimationIsReachable(t *testing.T) {
	app := readUIAsset(t, "app.css")
	reduced := mediaBlock(t, app, "@media (prefers-reduced-motion:reduce)")
	requireUIRegex(t, reduced, `\*\s*,\s*\*::before\s*,\s*\*::after\s*\{[^}]*animation:none!important`)
	requireUIRegex(t, reduced, `transition:none!important`)
	requireUIRegex(t, reduced, `scroll-behavior:auto`)
	// Skeletons animate through the shared skel-pulse keyframes, so the global
	// reset above leaves them as a static block.
	requireUIContains(t, readUIAsset(t, "primitives.css"), ".skel{", "skel-pulse")
	// No animation may be forced on with !important, which would defeat the global reset.
	for _, name := range foundationStylesheets {
		body := stripCSSComments(readUIAsset(t, name))
		if regexp.MustCompile(`animation(?:-name)?\s*:[^;}]*!important`).MatchString(strings.ReplaceAll(body, "animation:none!important", "")) {
			t.Errorf("%s forces an animation with !important", name)
		}
	}
	// Every animation shorthand must name a keyframes block that exists.
	defined := map[string]bool{}
	for _, name := range foundationStylesheets {
		for _, m := range regexp.MustCompile(`@keyframes\s+([\w-]+)`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			defined[m[1]] = true
		}
	}
	for _, name := range foundationStylesheets {
		body := stripCSSComments(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`animation\s*:\s*([\w-]+)`).FindAllStringSubmatch(body, -1) {
			if m[1] == "none" || m[1] == "inherit" || m[1] == "initial" {
				continue
			}
			if !defined[m[1]] {
				t.Errorf("%s uses animation %q with no @keyframes", name, m[1])
			}
		}
	}
}

// --- Focus and target size -------------------------------------------------

func TestUIA11yFocusIsNeverRemovedWithoutReplacement(t *testing.T) {
	app := stripCSSComments(readUIAsset(t, "app.css"))
	requireUIRegex(t, app, `:focus-visible\s*\{[^}]*outline`)
	// Programmatic-focus containers (tabindex=-1) may drop the ring; nothing
	// else that is keyboard focusable may.
	allowed := map[string]bool{
		"#main:focus":           true,
		".find-workspace-panel": true,
		"#inspectSplitter:hover,#inspectSplitter:focus": true,
	}
	for _, name := range foundationStylesheets {
		body := stripCSSComments(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(body, -1) {
			if !regexp.MustCompile(`outline\s*:\s*(none|0)\b`).MatchString(m[2]) {
				continue
			}
			sel := strings.TrimSpace(m[1])
			if i := strings.LastIndex(sel, "}"); i >= 0 {
				sel = strings.TrimSpace(sel[i+1:])
			}
			if allowed[sel] {
				continue
			}
			// A rule that swaps the outline for a visible border/box-shadow/background is a replacement.
			if regexp.MustCompile(`(box-shadow|border(-color)?|background)\s*:`).MatchString(m[2]) && strings.Contains(sel, ":focus") {
				continue
			}
			// Text fields draw their own focus border; checked separately below.
			if regexp.MustCompile(`^(input|textarea|select|\.[\w-]+)`).MatchString(sel) && strings.Contains(m[2], "box-shadow") {
				continue
			}
			t.Errorf("%s removes the outline for %q without a visible replacement", name, sel)
		}
	}
}

func TestUIA11yPointerCoarseRaisesTargetsInEveryInteractiveSheet(t *testing.T) {
	for _, name := range []string{"app.css", "primitives.css", "surfaces.css", "shell.css", "flow.css", "findings.css", "settings.css"} {
		body := readUIAsset(t, name)
		if !strings.Contains(body, "pointer:coarse") && !strings.Contains(body, "pointer: coarse") {
			t.Errorf("%s has no @media (pointer:coarse) rule; new interactives need 44px touch targets", name)
		}
	}
	// mobile.css sizes its controls statically (always phone width); report.css
	// controls are .btn, which the app.css coarse rule raises to 44px.
	mobile := readUIAsset(t, "mobile.css")
	requireUIRegex(t, mobile, `\.dock-btn\{[^}]*min-height:44px`)
	requireUIContains(t, mobile, "--bottomnav-h")
	app := readUIAsset(t, "app.css")
	requireUIRegex(t, app, `--hit-min:\s*44px`)
	requireUIRegex(t, app, `--row-h:\s*44px`)
}

func TestUIA11yStickyBarsDoNotObscureFocus(t *testing.T) {
	app := readUIAsset(t, "app.css")
	requireUIRegex(t, app, `--sticky-top:\s*calc\(var\(--topbar-h\)\s*\+\s*var\(--ctxbar-h\)\)`)
	requireUIRegex(t, app, `scroll-padding-top:\s*var\(--sticky-top\)`)
}

// --- Markup contracts ------------------------------------------------------

func uiTags(body, tag string) []string {
	return regexp.MustCompile(`(?s)<`+tag+`\b[^>]*>`).FindAllString(body, -1)
}

func TestUIA11yIndexIDsAreUniqueAndReferencesResolve(t *testing.T) {
	index := readUIAsset(t, "index.html")
	seen := map[string]int{}
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(index, -1) {
		seen[m[1]]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("id %q appears %d times in index.html", id, n)
		}
	}
	// Ids created by scripts at runtime are valid reference targets too.
	var scripts strings.Builder
	for _, name := range uiScriptNames(t) {
		scripts.WriteString(readUIAsset(t, name))
	}
	js := scripts.String()
	for _, m := range regexp.MustCompile(`\s(aria-(?:labelledby|describedby|controls|owns)|for)="([^"]+)"`).FindAllStringSubmatch(index, -1) {
		for _, id := range strings.Fields(m[2]) {
			if seen[id] == 0 && !strings.Contains(js, id) {
				t.Errorf("index.html %s=%q references an id that exists nowhere", m[1], id)
			}
		}
	}
}

func TestUIA11yEveryStaticControlHasAnAccessibleName(t *testing.T) {
	index := readUIAsset(t, "index.html")
	buttons := regexp.MustCompile(`(?s)<button\b([^>]*)>(.*?)</button>`).FindAllStringSubmatch(index, -1)
	if len(buttons) < 50 {
		t.Fatalf("only %d buttons parsed from index.html; the audit regex is stale", len(buttons))
	}
	named := regexp.MustCompile(`aria-label(?:ledby)?="[^"]+"|\stitle="[^"]+"`)
	for _, m := range buttons {
		text := regexp.MustCompile(`(?s)<svg\b.*?</svg>|<[^>]+>`).ReplaceAllString(m[2], "")
		// A hidden button is filled by its owner before it is shown (the Activity "N new" pill).
		if strings.TrimSpace(text) == "" && !named.MatchString(m[1]) && !regexp.MustCompile(`\shidden\b`).MatchString(m[1]) {
			t.Errorf("button with no text and no aria-label/title: <button %s>", strings.TrimSpace(m[1]))
		}
	}
	labelFor := map[string]bool{}
	for _, m := range regexp.MustCompile(`<label\b[^>]*\sfor="([^"]+)"`).FindAllStringSubmatch(index, -1) {
		labelFor[m[1]] = true
	}
	// Wrapped controls: <label>...<input ...></label>.
	wrapped := regexp.MustCompile(`(?s)<label\b[^>]*>.*?</label>`).FindAllString(index, -1)
	var wrappedHTML = strings.Join(wrapped, "\n")
	for _, tag := range []string{"input", "select", "textarea"} {
		for _, el := range uiTags(index, tag) {
			if regexp.MustCompile(`type="hidden"|\shidden\b`).MatchString(el) {
				continue
			}
			id := ""
			if m := regexp.MustCompile(`\sid="([^"]+)"`).FindStringSubmatch(el); m != nil {
				id = m[1]
			}
			if named.MatchString(el) || (id != "" && (labelFor[id] || strings.Contains(wrappedHTML, el))) {
				continue
			}
			t.Errorf("<%s> has no label, aria-label or title: %s", tag, strings.TrimSpace(el))
		}
	}
}

func TestUIA11yDialogsAndRegionsAreLabelled(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, el := range uiTags(index, "div") {
		if !regexp.MustCompile(`role="(dialog|alertdialog)"`).MatchString(el) {
			continue
		}
		if !strings.Contains(el, "aria-modal=") {
			t.Errorf("dialog without aria-modal: %s", el)
		}
		if !regexp.MustCompile(`aria-label(ledby)?=`).MatchString(el) {
			t.Errorf("dialog without an accessible name: %s", el)
		}
	}
	for _, el := range append(uiTags(index, "div"), uiTags(index, "section")...) {
		if regexp.MustCompile(`role="(region|complementary|navigation)"`).MatchString(el) && !regexp.MustCompile(`aria-label(ledby)?=`).MatchString(el) {
			t.Errorf("landmark without a name: %s", el)
		}
	}
	for _, el := range uiTags(index, "nav") {
		if !strings.Contains(el, "aria-label") {
			t.Errorf("nav without aria-label: %s", el)
		}
	}
	if n := strings.Count(index, `<main`); n != 1 {
		t.Errorf("index.html has %d <main> landmarks, want exactly 1", n)
	}
}

// --- Live regions and shortcuts -------------------------------------------

func TestUIA11yLiveRegionsAreRateLimited(t *testing.T) {
	state := readUIAsset(t, "js/project-state.js")
	requireUIContains(t, state, "ANNOUNCE_GAP_MS = 5000", "FLOW_NEW_THROTTLE_MS = 2000")
	ctx := readUIAsset(t, "js/ctxbar.js")
	requireUIContains(t, ctx, "createBlockerAnnouncer")
	requireUIContains(t, readUIAsset(t, "js/connection.js"), "OFFLINE_BANNER_MS = 5000")
	// Assertive announcements are for errors only: no new module may add an
	// unconditional aria-live="assertive" region.
	for _, name := range uiScriptNames(t) {
		for _, m := range regexp.MustCompile(`aria-live['"]\s*,\s*['"]assertive['"]|aria-live="assertive"`).FindAllString(readUIAsset(t, name), -1) {
			if !strings.Contains(readUIAsset(t, name), "error") {
				t.Errorf("%s declares an assertive live region (%s) outside an error path", name, m)
			}
		}
	}
}

func TestUIA11ySingleKeyRegistrationsNeverConflictAndAreGated(t *testing.T) {
	reg := regexp.MustCompile(`keys:\s*'([^']+)'\s*,\s*scope:\s*'([^']+)'|keys:'([^']+)',scope:'([^']+)'`)
	seen := map[string]string{}
	for _, name := range uiScriptNames(t) {
		for _, m := range reg.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			keys, scope := m[1], m[2]
			if keys == "" {
				keys, scope = m[3], m[4]
			}
			k := scope + "|" + keys
			if prev, ok := seen[k]; ok {
				t.Errorf("%s binds %q in scope %q already bound in %s", name, keys, scope, prev)
			}
			seen[k] = name
		}
	}
	if len(seen) < 6 {
		t.Fatalf("found only %d scoped registrations; the audit regex is stale", len(seen))
	}
	keys := readUIAsset(t, "js/keys.js")
	requireUIContains(t, keys, "isTypingTarget", "singleKey")
	// New single-key bindings must also have a visible button: the attach and
	// diff verbs ship as toolbar buttons in the same modules.
	requireUIContains(t, readUIAsset(t, "js/evidence-attach.js"), "Attach as evidence")
}

// --- Regression checklist (section 11) ------------------------------------

func TestUIA11yPreservedShellIDsAndTabs(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"tabs", "crumb", "mobileToolSelect", "sseStatus", "proxyAddr", "controlAddr", "deviceProxyChip", "findReadinessBoard", "cmdkBtn", "setNav", "ctxbar", "dock", "main"} {
		if !strings.Contains(index, `id="`+id+`"`) && !strings.Contains(readUIAsset(t, "js/finding-readiness-board.js")+readUIAsset(t, "js/findings.js"), id) {
			t.Errorf("preserved id #%s is gone", id)
		}
	}
	for _, tab := range []string{"proxy", "intercept", "repeater", "intruder", "scanner", "map", "findings", "notes", "activity", "settings"} {
		if !strings.Contains(index, `data-tab="`+tab+`"`) {
			t.Errorf("rail tab data-tab=%q is gone", tab)
		}
	}
}

func TestUIA11yLegacyModalsStayRegisteredAndReachable(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	ids := regexp.MustCompile(`MODAL_IDS=\[([^\]]+)\]`).FindStringSubmatch(core)
	if ids == nil {
		t.Fatal("MODAL_IDS not found")
	}
	for _, id := range []string{"findCreateModal", "findPickModal", "findFlowPickModal", "findExportModal", "compareModal", "findGuideModal", "findDeletedModal"} {
		if !strings.Contains(ids[1], "'"+id+"'") {
			t.Errorf("legacy modal %s dropped from MODAL_IDS", id)
		}
	}
	// The palette lists every modal id as an action (reversibility).
	palette := readUIAsset(t, "js/cmdk-logic.js")
	requireUIContains(t, readUIAsset(t, "js/cmdk-actions.js"), "legacyModalCommands", "LEGACY_MODALS")
	for _, id := range []string{"findCreateModal", "findPickModal", "findFlowPickModal", "findExportModal", "compareModal"} {
		if !strings.Contains(palette, id) {
			t.Errorf("legacy dialog %s is not reachable from the command palette", id)
		}
	}
}

func TestUIA11yLockedSurfaceCarriesNoEngagementData(t *testing.T) {
	login := readUIAsset(t, "login.html")
	for _, leak := range []string{"ctxbar", "ctxTarget", "projBadge", "connChip", "id=\"dock\"", "findReadinessBoard", "/api/project/readiness", "/api/findings", "/api/flows"} {
		if strings.Contains(login, leak) {
			t.Errorf("login.html (the locked state) references %q", leak)
		}
	}
	// The shell strip fetches nothing before hydration.
	requireUIContains(t, readUIAsset(t, "js/app.js"), "function refreshProjectState(reason){if(projectScopedUIReady)projectState.refresh({reason});}")
}

func TestUIA11yRailAndDockNeverBothExposed(t *testing.T) {
	requireUIRegex(t, stripCSSComments(readUIAsset(t, "app.css")+readUIAsset(t, "mobile.css")), `#tabs\s*\{\s*display:none`)
}

// --- Budgets and docs ------------------------------------------------------

// TestUIA11yModuleSizeBudget keeps the embedded UI inside the "no build step,
// small modules" constraint: legacy feature modules may stay large, but every
// module added by the overhaul stays under a documented line budget and the
// whole embedded surface stays under a byte budget.
func TestUIA11yModuleSizeBudget(t *testing.T) {
	const newModuleLines = 700
	legacy := map[string]bool{
		"js/proxy.js": true, "js/settings.js": true, "js/tools.js": true, "js/findings.js": true,
		"js/core.js": true, "js/map.js": true, "js/scanner.js": true, "js/app.js": true,
		"js/intercept.js": true, "js/apipanel.js": true, "js/authz.js": true,
	}
	for _, name := range uiScriptNames(t) {
		if legacy[name] {
			continue
		}
		if n := strings.Count(readUIAsset(t, name), "\n"); n > newModuleLines {
			t.Errorf("%s is %d lines (budget %d for modules added by the overhaul); split pure logic into a *-model.js file", name, n, newModuleLines)
		}
	}
	var total int
	err := fs.WalkDir(uiFS, "ui", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := uiFS.ReadFile(p)
		total += len(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if const_budget := 3 << 20; total > const_budget {
		t.Errorf("embedded UI is %d bytes; budget %d", total, const_budget)
	}
}

func TestUIA11yArchitectureDocNamesEveryOverhaulModule(t *testing.T) {
	b, err := os.ReadFile("../../docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	at := strings.Index(doc, "## Web UI")
	if at < 0 {
		t.Fatal("docs/architecture.md has no Web UI section")
	}
	web := doc[at:]
	for _, name := range []string{
		"project-state.js", "ctxbar.js", "connection.js", "cmdk.js", "keyboard.js", "keys.js", "split.js", "sheet.js",
		"statepanel.js", "diff.js", "copyas.js", "finder.js", "flowdrawer.js", "flowbody.js", "evidence-attach.js",
		"evidence-tray.js", "readiness-meter.js", "report-preflight.js", "checklist.js", "dock.js",
		"workbench.css", "primitives.css", "shell.css", "flow.css", "mobile.css", "report.css", "settings.css",
		"data-density", "data-theme", "--sticky-top", "_js-tests", "GET /api/project/readiness",
	} {
		if !strings.Contains(web, name) {
			t.Errorf("docs/architecture.md Web UI section does not mention %s", name)
		}
	}
}
