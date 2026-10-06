package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP4 (UI overhaul): command palette v2 and the keyboard model. Pure logic runs
// under node (ui/_js-tests/cmdk-logic.test.mjs, shortcuts-table.test.mjs);
// everything DOM-shaped is asserted statically against the embedded assets.

var cmdkModules = []string{"js/cmdk.js", "js/cmdk-logic.js", "js/cmdk-actions.js", "js/keyboard.js", "js/shortcuts-table.js"}

func TestUICmdkPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/cmdk-logic.test.mjs", "_js-tests/shortcuts-table.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUICmdkModulesResolveImportsAndStayPureWhereRequired(t *testing.T) {
	imp := regexp.MustCompile(`(?:import|from)\s*\(?\s*'\./([a-z-]+\.js)'`)
	for _, name := range cmdkModules {
		for _, m := range imp.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			readUIAsset(t, "js/"+m[1]) // fails the test when the module is not embedded
		}
	}
	for _, name := range []string{"js/cmdk-logic.js", "js/shortcuts-table.js"} {
		src := readUIAsset(t, name)
		if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(src) || strings.Contains(src, "document.") || strings.Contains(src, "window.") {
			t.Errorf("%s must stay DOM-free and not import core.js so node can load it", name)
		}
	}
}

func TestUICmdkModulesUseNoInlineStyles(t *testing.T) {
	for _, name := range cmdkModules {
		src := executableJS(readUIAsset(t, name))
		for _, bad := range []string{"style=", "cssText", "setProperty(", "setAttribute('style'"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses inline styling (%s); use classes", name, bad)
			}
		}
		// Reading element.style.display (open-dialog checks) is fine; writing is not.
		if regexp.MustCompile(`\.style\.[A-Za-z]+\s*=[^=]`).MatchString(src) {
			t.Errorf("%s assigns inline styles; use classes or the hidden attribute", name)
		}
	}
}

func TestUICmdkNeverAssignsUnescapedHTMLInKeyboardModule(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/keyboard.js"))
	if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
		t.Error("keyboard.js must build the chord popup and shortcut sheet with textContent")
	}
}

func TestUICmdkComboboxAccessibilityContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/cmdk.js"))
	requireUIContains(t, src,
		`role="combobox"`, `aria-controls="cmdkList"`, `aria-expanded="true"`, `aria-autocomplete="list"`,
		`aria-describedby="cmdkHint"`, `aria-activedescendant`, `role="listbox"`, `role="option"`,
		`role="group" aria-labelledby=`,
		`id="cmdkLive" class="u-sr" role="status" aria-live="polite"`,
		"resultCountText(",
		// Esc closes the palette only, never an underlying dialog.
		"e.preventDefault();e.stopPropagation();cmdkClose()",
		"e.shiftKey",
	)
	// The result count is announced on a delay so typing does not chatter.
	requireUIRegex(t, src, `setTimeout\(\(\)=>\{if\(cmdk\.live\)cmdk\.live\.textContent=resultCountText`)
	// Hovering moves the selection without repainting (a repaint would eat the click).
	requireUIContains(t, src, "function cmdkSelectOnly()")
}

func TestUICmdkPrefixModesAndGroups(t *testing.T) {
	logic := executableJS(readUIAsset(t, "js/cmdk-logic.js"))
	requireUIContains(t, logic,
		"startsWith('>')", "startsWith('?')", "startsWith('#')", "startsWith('@')", "/^f:/i",
		"export const MAX_ROWS = 50", "export const RECENTS_MAX = 20",
	)
}

func TestUICmdkLogicUsesTheSharedFuzzyScorer(t *testing.T) {
	logic := executableJS(readUIAsset(t, "js/cmdk-logic.js"))
	requireUIContains(t, logic, "import { fuzzyScore } from './keys.js'")
	if strings.Contains(logic, "export function fuzzyScore") {
		t.Error("cmdk-logic.js must reuse keys.js fuzzyScore, not define its own")
	}
}

func TestUICmdkRecentsAreGuardedAndVersioned(t *testing.T) {
	logic := executableJS(readUIAsset(t, "js/cmdk-logic.js"))
	requireUIContains(t, logic, "interseptor.cmdkRecents.v1")
	for _, fn := range []string{"export function loadRecents", "export function saveRecents"} {
		i := strings.Index(logic, fn)
		if i < 0 {
			t.Fatalf("missing %s", fn)
		}
		body := logic[i : i+400]
		if !strings.Contains(body, "try {") || !strings.Contains(body, "catch") {
			t.Errorf("%s must wrap storage access in try/catch", fn)
		}
	}
}

func TestUICmdkPaletteListsEveryLegacyDialog(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	m := regexp.MustCompile(`export const MODAL_IDS=\[([^\]]*?)\n`).FindStringSubmatch(core)
	if m == nil {
		t.Fatal("MODAL_IDS not found")
	}
	legacyBlock := m[1]
	if i := strings.Index(legacyBlock, "//"); i >= 0 {
		legacyBlock = legacyBlock[:i]
	}
	ids := regexp.MustCompile(`'([A-Za-z]+)'`).FindAllStringSubmatch(legacyBlock, -1)
	if len(ids) < 20 {
		t.Fatalf("expected the 21 legacy dialog ids, parsed %d", len(ids))
	}
	logic := readUIAsset(t, "js/cmdk-logic.js")
	app := readUIAsset(t, "js/app.js")
	index := readUIAsset(t, "index.html")
	var sources strings.Builder
	for _, name := range []string{"js/findings.js", "js/finding-workspace.js"} {
		sources.WriteString(readUIAsset(t, name))
	}
	for _, id := range ids {
		entry := regexp.MustCompile(`\{ id: '` + id[1] + `'[^\n]*\},?\n`).FindString(logic)
		if entry == "" {
			t.Errorf("legacy dialog %s is not listed in LEGACY_MODALS", id[1])
			continue
		}
		if c := regexp.MustCompile(`cmd: '([^']+)'`).FindStringSubmatch(entry); c != nil {
			if !strings.Contains(app, "{t:'"+c[1]+"'") && !strings.Contains(app, "t:'"+c[1]+"'") {
				t.Errorf("%s routes to base command %q which app.js no longer offers", id[1], c[1])
			}
		}
		if c := regexp.MustCompile(`click: '#([A-Za-z]+)'`).FindStringSubmatch(entry); c != nil {
			if !strings.Contains(index, `id="`+c[1]+`"`) && !strings.Contains(sources.String(), c[1]) {
				t.Errorf("%s routes to #%s which does not exist", id[1], c[1])
			}
		}
		if strings.Contains(entry, "transient:") && !regexp.MustCompile(`transient: '[^']{8,}'`).MatchString(entry) {
			t.Errorf("%s is marked transient without a reason", id[1])
		}
	}
	actions := executableJS(readUIAsset(t, "js/cmdk-actions.js"))
	requireUIContains(t, actions, "export function legacyModalCommands()", "...legacyModalCommands()")
}

func TestUICmdkActionsAreNavigationOrReversible(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/cmdk-actions.js") + readUIAsset(t, "js/cmdk.js"))
	for _, bad := range []string{"method:'DELETE'", "method: 'DELETE'", "method:'POST'", "method: 'POST'", "method:'PUT'", "method: 'PUT'", "apiTry(", "runScan", "intrStart", "repSend", "toggleIntercept"} {
		if strings.Contains(src, bad) {
			t.Errorf("palette code contains %s; palette actions must navigate, open dialogs or toggle reversibly", bad)
		}
	}
	// The one asynchronous read is the findings list for `#`, fetched lazily.
	if n := strings.Count(src, "api('/api/"); n != 2 {
		t.Errorf("palette makes %d API reads, want exactly the findings list and the flow search", n)
	}
	requireUIContains(t, src, "api('/api/findings')", "'/api/flows?limit=20&search='")
}

func TestUICmdkShiftEnterSendsFlowToRepeaterAndEnterOpensDrawer(t *testing.T) {
	cmdk := executableJS(readUIAsset(t, "js/cmdk.js"))
	actions := executableJS(readUIAsset(t, "js/cmdk-actions.js"))
	requireUIContains(t, cmdk, "openFlowFor(it.ref,{toRepeater:!!opts.shift,fallback:cmdkEnv.openFlow})")
	requireUIContains(t, actions, "openFlow(flow.id, { source: 'palette' })", "m.sendToRepeater(flow)")
}

func TestUICmdkOpenKeepsGuardsAndPrefill(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/cmdk.js"))
	requireUIContains(t, src,
		"if(cmdkEnv.blocked())return;",
		"if(!cmdkEnv.ready()){toast('Loading saved workspace…');return;}",
		"export function cmdkOpenWith(", "cmdk.input.value=cmdk.prefill||''",
	)
}

func TestUILegacyShortcutTableMatchesHandlers(t *testing.T) {
	table := readUIAsset(t, "js/shortcuts-table.js")
	files := map[string]string{
		"app.js":   readUIAsset(t, "js/app.js"),
		"proxy.js": readUIAsset(t, "js/proxy.js"),
		"tools.js": readUIAsset(t, "js/tools.js"),
	}
	row := regexp.MustCompile(`L\('([^']+)', '([^']+)', '([^']+)', '[^']+', '([a-z]+\.js)', ("[^"]+"|` + "`[^`]+`" + `)\)`)
	rows := row.FindAllStringSubmatch(table, -1)
	if len(rows) < 18 {
		t.Fatalf("parsed %d legacy rows, want at least 18", len(rows))
	}
	for _, r := range rows {
		probe := strings.Trim(r[5], "\"`")
		if strings.Contains(probe, "${") {
			continue // generated go-to rows are checked through GO_MNEMONICS below
		}
		if !strings.Contains(files[r[4]], probe) {
			t.Errorf("legacy shortcut %q: probe %q not found in %s (handler changed? update shortcuts-table.js)", r[3], probe, r[4])
		}
	}

	// Inverse: every plain or modified letter bound in app.js appears in the table.
	appKeys := regexp.MustCompile(`is(?:Plain|Mod)Shortcut\(e,'([^']+)'`).FindAllStringSubmatch(files["app.js"], -1)
	for _, k := range appKeys {
		key := k[1]
		if key == "k" || key == "Enter" {
			if !strings.Contains(table, "'Ctrl+K'") && key == "k" {
				t.Error("Ctrl+K missing from the legacy table")
			}
			continue
		}
		if !regexp.MustCompile(`(?i)'(?:Ctrl\+)?` + regexp.QuoteMeta(key) + `'|'[^']*\b` + regexp.QuoteMeta(key) + `\b[^']*'`).MatchString(table) {
			t.Errorf("app.js binds %q but shortcuts-table.js does not list it", key)
		}
	}
	mn := regexp.MustCompile(`GO_MNEMONICS=\{([^}]*)\}`).FindStringSubmatch(files["app.js"])
	if mn == nil {
		t.Fatal("GO_MNEMONICS not found in app.js")
	}
	for _, g := range regexp.MustCompile(`([a-z]):'([a-z]+)'`).FindAllStringSubmatch(mn[1], -1) {
		if !strings.Contains(table, "['"+g[1]+"', '") {
			t.Errorf("go-to chord g %s is missing from the legacy table", g[1])
		}
	}
}

func TestUIKeyRegistrationsDoNotConflict(t *testing.T) {
	legacy := map[string]bool{}
	table := readUIAsset(t, "js/shortcuts-table.js")
	for _, r := range regexp.MustCompile(`L\('[^']+', '([^']+)', '([^']+)',`).FindAllStringSubmatch(table, -1) {
		legacy[r[1]+"|"+r[2]] = true
	}
	reg := regexp.MustCompile(`(?s)\.register\(\{(.*?)\}\)`)
	idRe := regexp.MustCompile(`id:\s*'([^']+)'`)
	keysRe := regexp.MustCompile(`keys:\s*'([^']+)'`)
	scopeRe := regexp.MustCompile(`scope:\s*'([^']+)'`)
	seen := map[string]string{}
	for _, name := range allUIJSNames(t) {
		if name == "js/keys.js" {
			continue
		}
		for _, m := range reg.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			id, keys := idRe.FindStringSubmatch(m[1]), keysRe.FindStringSubmatch(m[1])
			if id == nil || keys == nil {
				continue
			}
			scope := "global"
			if s := scopeRe.FindStringSubmatch(m[1]); s != nil {
				scope = s[1]
			}
			k := scope + "|" + keys[1]
			if prev, dup := seen[k]; dup {
				t.Errorf("%s registers %q in scope %s, already bound by %s", id[1], keys[1], scope, prev)
			}
			seen[k] = id[1]
			if legacy[k] {
				t.Errorf("%s registers %q in scope %s, which a legacy handler already owns", id[1], keys[1], scope)
			}
		}
	}
}

func allUIJSNames(t *testing.T) []string {
	t.Helper()
	entries, err := uiFS.ReadDir("ui/js")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			names = append(names, "js/"+e.Name())
		}
	}
	return names
}

func TestUIKeyboardModuleContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/keyboard.js"))
	requireUIContains(t, src,
		"createKeyRegistry({", "singleKeyEnabled: singleKeyShortcutsOn", "isModalOpen: modalOpen", "onPendingChange: renderChordHint",
		"registerHook('keyRegistry'", "registerHook('singleKeyShortcuts'",
		"export const SINGLE_KEY_EVENT = 'interseptor:singlekeychange'",
		"export function setSingleKeyShortcuts(on)",
		"writeSingleKeyPref(singleKey)",
		// One dispatcher, never during the palette, never over a handled event.
		"if (e.defaultPrevented || paletteOpen()) return;",
		"keyRegistry.handle(e, { scopes: activeScopes() })",
		`setAttribute('role', 'status')`, `setAttribute('aria-live', 'polite')`,
		"MutationObserver", "renderShortcutSheet(modal.querySelector('.sc-grid'))",
	)
	if n := strings.Count(src, "addEventListener('keydown'"); n != 1 {
		t.Errorf("keyboard.js registers %d keydown listeners, want exactly one dispatcher", n)
	}
	// The modal block keeps the static fallback cards and gains the WCAG 2.1.4 switch.
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="scSingleKey"`, `class="sc-grid"`, "Find inside the inspected request or response")
	if strings.Count(index, `id="scSingleKey"`) != 1 {
		t.Error("scSingleKey must exist exactly once")
	}
}

func TestUICmdkStylesCoverNewClassesAndPhoneSheet(t *testing.T) {
	css := readUIAsset(t, "surfaces.css")
	for _, sel := range []string{".cmdk-hint", ".cmdk-group", ".cmdk-modes", ".chord-hint", ".chord-next", ".chord-cancel", ".sc-foot-switch"} {
		if !strings.Contains(css, sel) {
			t.Errorf("surfaces.css missing %s", sel)
		}
	}
	i := strings.Index(css, "Phones: the palette becomes a full-height sheet")
	if i < 0 {
		t.Fatal("phone palette block missing")
	}
	phone := css[i:]
	phone = phone[:strings.Index(phone, "/* Chord continuation popup")]
	requireUIContains(t, phone, "@media (max-width:720px)", "height:100dvh", "min-height:48px", "var(--safe-t)", "var(--safe-b)", ".cmdk-modes{display:none}")
	// New rules animate nothing: the chord popup and palette appear instantly.
	block := css[strings.Index(css, ".cmdk-hint"):]
	block = block[:strings.Index(block, ".sc-foot-switch")]
	if strings.Contains(block, "transition") || strings.Contains(block, "animation") || strings.Contains(block, "@keyframes") {
		t.Error("palette and chord styles must not animate")
	}
	if strings.Contains(block, "style=") {
		t.Error("palette CSS block looks malformed")
	}
	// Every custom property used by the new rules is defined.
	app := readUIAsset(t, "app.css")
	for _, v := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(block, -1) {
		if !strings.Contains(app, v[1]+":") && !strings.Contains(app, v[1]+" :") {
			t.Errorf("new palette CSS uses undefined token %s", v[1])
		}
	}
}
