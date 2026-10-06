package control

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// WP1 (UI overhaul core primitives): SplitPane, BottomSheet, StatePanel, DiffView,
// Copy-as, Finder, the keyboard registry and the core.js hooks. The UI has no
// build step, so pure logic runs under `node --test` (ui/_js-tests, not embedded)
// and everything DOM-shaped is asserted statically against the embedded assets.

var primitiveModules = []string{
	"js/layout-math.js", "js/keys.js", "js/split.js", "js/sheet.js", "js/statepanel.js",
	"js/diff.js", "js/copyas.js", "js/finder.js",
}

func TestUIPrimitivesPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	// Pass an explicit file list: Node 21+ treats a directory argument as a module
	// path ("Cannot find module .../_js-tests"), and only Node 20 recursed into it.
	files, err := filepath.Glob(filepath.Join("ui", "_js-tests", "*.test.mjs"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no node test files found under ui/_js-tests: %v", err)
	}
	for i, f := range files {
		files[i] = filepath.ToSlash(strings.TrimPrefix(f, "ui"+string(filepath.Separator)))
	}
	cmd := exec.Command(node, append([]string{"--test"}, files...)...)
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUIPrimitivesJSTestsAreNotEmbedded(t *testing.T) {
	if _, err := fs.Stat(uiFS, "ui/_js-tests"); err == nil {
		t.Fatal("ui/_js-tests must stay out of the embedded UI (leading underscore is excluded by go:embed)")
	}
}

func TestUIPrimitiveModulesResolveTheirImports(t *testing.T) {
	imp := regexp.MustCompile(`(?:import|from)\s*\(?\s*'\./([a-z-]+\.js)'`)
	for _, name := range primitiveModules {
		for _, m := range imp.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			if _, err := fs.Stat(uiFS, "ui/js/"+m[1]); err != nil {
				t.Errorf("%s imports ./%s which is not embedded", name, m[1])
			}
		}
	}
}

// The pure modules must not import core.js (it touches the DOM at load), or the
// node tests could not load them.
func TestUIPureModulesStayDOMFree(t *testing.T) {
	for _, name := range []string{"js/layout-math.js", "js/keys.js", "js/diff.js", "js/copyas.js", "js/finder.js", "js/split.js"} {
		if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(readUIAsset(t, name)) {
			t.Errorf("%s statically imports core.js; keep it loadable under node", name)
		}
	}
}

func TestUIPrimitivesNeverWriteInlineStyleOrUnsafeHTML(t *testing.T) {
	for _, name := range primitiveModules {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "style='") || strings.Contains(src, "cssText") {
			t.Errorf("%s writes inline style markup; use classes and custom properties", name)
		}
		if name != "js/statepanel.js" && regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
			t.Errorf("%s assigns HTML strings; build nodes with textContent", name)
		}
	}
	// statepanel's single innerHTML is the fixed sprite reference from icon().
	if n := strings.Count(executableJS(readUIAsset(t, "js/statepanel.js")), "innerHTML"); n != 1 {
		t.Errorf("statepanel.js innerHTML uses = %d, want exactly the icon() sprite reference", n)
	}
}

func TestUISplitPaneContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/split.js"))
	requireUIContains(t, src,
		"export function createSplitPane(",
		"setAttribute('role', 'separator')", "sash.tabIndex = 0",
		"aria-orientation", "aria-valuenow", "aria-valuemin", "aria-valuemax",
		"setPointerCapture", "requestAnimationFrame", "'--split-size'",
		"splitKeyAction(", "'dblclick'",
		"transitionend", "ResizeObserver",
		"data-split-focus", "split-back", "popstate", "history.pushState", "ignorePop",
		"parseStoredPercent", "scopeKey",
	)
	// persistence is guarded
	requireUIRegex(t, src, `(?s)function load\(\).*?try\s*\{.*?catch`)
	requireUIRegex(t, src, `(?s)function save\(\).*?try\s*\{.*?catch`)
	// one history entry only
	if strings.Count(src, "pushState(") != 1 {
		t.Error("split.js must push at most one guarded history entry")
	}
	css := readUIAsset(t, "primitives.css")
	requireUIContains(t, css, `.split[data-mode="stack"]`, ".split-sash", ".split-back", "touch-action:none")
}

func TestUIBottomSheetContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/sheet.js"))
	requireUIContains(t, src,
		"export function openSheet(", "export function closeSheet(",
		"'role', 'dialog'", "aria-modal", "'aria-label', 'Resize panel'",
		"openModal(", "closeModal(", "ArrowUp", "ArrowDown", "nextDetent(", "resolveSheetDrag(",
		"setPointerCapture", "watchSoftKeyboard(", "--sheet-drag", "transitionend",
	)
	requireUIRegex(t, src, `(?s)handle\.type = 'button'`)
	css := readUIAsset(t, "surfaces.css")
	requireUIContains(t, css, ".sheet-root", ".sheet-handle", `.sheet[data-detent="peek"]`, `.sheet[data-detent="half"]`, `.sheet[data-detent="full"]`, "touch-action:none", `#dock`)
	// the handle (not the sheet) owns touch-action:none so the body still scrolls
	if regexp.MustCompile(`\.sheet\{[^}]*touch-action:none`).MatchString(css) {
		t.Error("touch-action:none belongs on .sheet-handle only")
	}
}

func TestUIStatePanelContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/statepanel.js"))
	requireUIContains(t, src,
		"export function renderState(", "empty-first", "empty-filtered", "loading", "'error'", "offline", "locked",
		"aria-busy", "clampSkeletonRows", "Clear filters", "Retry", "Copy details", "role', 'status'",
	)
	// locked state: only the generic lock card, no project or host details
	requireUIRegex(t, src, `(?s)kind === 'locked'.*?heading\.textContent = 'Locked'`)
	css := readUIAsset(t, "primitives.css")
	requireUIContains(t, css, ".skel", "@keyframes skel-pulse", ".state-actions")
}

func TestUICopyAsKindsAndShortcutLetters(t *testing.T) {
	src := readUIAsset(t, "js/copyas.js")
	for _, kind := range []string{"curl", "curl-ps", "fetch", "python", "go", "httpie", "raw", "har", "har-bundle", "url", "headers", "body", "markdown", "repro"} {
		if !strings.Contains(src, "kind: '"+kind+"'") {
			t.Errorf("copy-as kind %q missing", kind)
		}
	}
	// y + letter: c curl, f fetch, p python, r raw, u url, m markdown, h HAR; letters are unique
	letters := map[string]string{}
	for _, m := range regexp.MustCompile(`\{ kind: '([a-z-]+)', label: '[^']+', key: '([a-z])'`).FindAllStringSubmatch(src, -1) {
		if prev, dup := letters[m[2]]; dup {
			t.Errorf("copy-as letter %q used by %s and %s", m[2], prev, m[1])
		}
		letters[m[2]] = m[1]
	}
	for letter, kind := range map[string]string{"c": "curl", "f": "fetch", "p": "python", "r": "raw", "u": "url", "m": "markdown", "h": "har"} {
		if letters[letter] != kind {
			t.Errorf("y %s must copy as %s, got %q", letter, kind, letters[letter])
		}
	}
	requireUIContains(t, src, "MAX_BODY_BYTES = 1024 * 1024", "reader.cancel()", "body truncated at 1 MB")
	// lazy: core.js is only imported on demand
	requireUIContains(t, src, "await import('./core.js')")
}

func TestUIFinderContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/finder.js"))
	requireUIContains(t, src,
		"export function createFinder(", "export function findMatches(", "MAX_HIGHLIGHTS = 5000",
		"createTreeWalker", "surroundContents", "createElement('mark')", "aria-live", "'role', 'search'",
		"e.altKey", "refine search", "aria-pressed",
	)
}

func TestUIDiffViewContract(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/diff.js"))
	requireUIContains(t, src,
		"MAX_DIFF_LINES = 20000", "RENDER_CHUNK = 500", "requestAnimationFrame", "diff-expand", "Expand ${hidden.count}",
		"glyphOf", "createDocumentFragment", "textContent", "aria-live",
	)
	css := readUIAsset(t, "primitives.css")
	requireUIContains(t, css, ".diff-add", ".diff-del", "var(--diff-add-bg)", "var(--diff-del-fg)", "var(--diff-word-add)")
}

func TestUIKeyRegistryGuards(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/keys.js"))
	requireUIContains(t, src,
		"export function createKeyRegistry(", "CHORD_TIMEOUT_MS = 1200", "already bound", "conflicts with chord prefix",
		"isTypingTarget", "isModalOpen", "singleKeyEnabled", "SINGLE_KEY_PREF", "export function fuzzyScore(", "export function fuzzyRank(", "cheatsheet(",
	)
	// registry writes the preference inside try/catch
	requireUIRegex(t, src, `(?s)function readSingleKeyPref.*?try.*?catch`)
	// Conflict policy: no two registrations in the same scope may share a binding or a chord prefix.
	reg := regexp.MustCompile(`keys\.register\(\{`)
	type bind struct {
		id, scope string
		steps     []string
	}
	var all []bind
	for _, name := range allUIJSFiles(t) {
		body := readUIAsset(t, name)
		for _, at := range reg.FindAllStringIndex(body, -1) {
			window := body[at[1]:]
			if len(window) > 600 {
				window = window[:600]
			}
			k := regexp.MustCompile(`keys:\s*'([^']+)'`).FindStringSubmatch(window)
			if k == nil {
				continue
			}
			scope := "global"
			if s := regexp.MustCompile(`scope:\s*'([^']+)'`).FindStringSubmatch(window); s != nil {
				scope = s[1]
			}
			all = append(all, bind{id: name, scope: scope, steps: strings.Fields(k[1])})
		}
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].scope != all[j].scope {
				continue
			}
			a, b := all[i].steps, all[j].steps
			n := len(a)
			if len(b) < n {
				n = len(b)
			}
			if strings.Join(a[:n], " ") == strings.Join(b[:n], " ") {
				t.Errorf("duplicate or shadowing key registration in scope %s: %v (%s) vs %v (%s)", all[i].scope, a, all[i].id, b, all[j].id)
			}
		}
	}
}

func allUIJSFiles(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(uiFS, "ui/js")
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

func TestUICoreModalIDsIncludeOverlaySurfaces(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	ids := regexp.MustCompile(`(?s)export const MODAL_IDS=\[(.*?)\];`).FindStringSubmatch(core)
	if ids == nil {
		t.Fatal("MODAL_IDS not found")
	}
	for _, want := range []string{"flowDrawer", "engagementSheet", "filtersSheet", "moreSheet", "detailSheet", "toolsSheet", "historySheet", "paletteSheet"} {
		if !strings.Contains(ids[1], "'"+want+"'") {
			t.Errorf("MODAL_IDS is missing overlay id %s", want)
		}
	}
	// every legacy modal stays registered (reversibility)
	for _, legacy := range []string{"flowModal", "findPickModal", "findFlowPickModal", "compareModal", "findCreateModal"} {
		if !strings.Contains(ids[1], "'"+legacy+"'") {
			t.Errorf("legacy modal %s was removed from MODAL_IDS", legacy)
		}
	}
	// any sheet id opened anywhere must be registered
	for _, name := range allUIJSFiles(t) {
		for _, m := range regexp.MustCompile(`openSheet\(\{\s*id:\s*'([A-Za-z]+)'`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			if !strings.Contains(ids[1], "'"+m[1]+"'") {
				t.Errorf("%s opens sheet %s which is not in MODAL_IDS", name, m[1])
			}
		}
	}
}

func TestUICoreHooksAndDensityExports(t *testing.T) {
	core := executableJS(readUIAsset(t, "js/core.js"))
	requireUIContains(t, core,
		"export function registerHook(", "export function openFlow(", "export function renderFlowBody(", "export function getHook(",
		"export function setDensity(", "export function getDensity(", "export function readRowHeightToken(",
		"new CustomEvent('densitychange'", "export function createDensityVirtualList(", "export function rowHeightToken(", "export function onDensityChange(",
		"addEventListener('densitychange'",
	)
}

func TestUICoreHooksAndVirtualListRemeasureUnderNode(t *testing.T) {
	requireNode(t)
	src := readUIAsset(t, "js/core.js")
	cut := func(from, to string) string {
		a := strings.Index(src, from)
		if a < 0 {
			t.Fatalf("core.js is missing %q", from)
		}
		b := strings.Index(src[a:], to)
		if b < 0 {
			t.Fatalf("core.js is missing %q after %q", to, from)
		}
		return strings.ReplaceAll(src[a:a+b], "export ", "")
	}
	density := cut("export const DENSITIES", "/* ---- extension registry")
	hooks := cut("const hooks=new Map()", "/* ---- createVirtualList")
	virt := cut("export function createVirtualList", "/* ---- createAutosave")
	script := `
const listeners={};let rowh='30',coarse=null;
globalThis.window={addEventListener:(n,f)=>{(listeners[n]=listeners[n]||[]).push(f)},dispatchEvent:e=>{(listeners[e.type]||[]).forEach(f=>f(e))},matchMedia:()=>({addEventListener:(n,f)=>{coarse=f}})};
globalThis.CustomEvent=class{constructor(type,init){this.type=type;this.detail=init&&init.detail}};
globalThis.localStorage={_v:{},setItem(k,v){this._v[k]=v},getItem(k){return this._v[k]}};
const root={_a:{},getAttribute(k){return this._a[k]??null},setAttribute(k,v){this._a[k]=v}};
globalThis.document={documentElement:root};
globalThis.getComputedStyle=()=>({getPropertyValue:()=>rowh});
` + density + hooks + virt + `
// registry
if(openFlow(5)!==false)throw new Error('openFlow without implementation must return false');
if(renderFlowBody({},'req')!==null)throw new Error('renderFlowBody without implementation must return null');
const off=registerHook('openFlow',(id,o)=>'opened:'+id+':'+o.source);
if(openFlow(5,{source:'proxy'})!=='opened:5:proxy')throw new Error('openFlow hook not called');
off();if(openFlow(5)!==false)throw new Error('unregister failed');
// density
if(getDensity()!=='default')throw new Error('default density');
let seen=null;
window.addEventListener('densitychange',e=>{seen=e.detail});
rowh='24';setDensity('compact');
if(root._a['data-density']!=='compact'||localStorage._v.density!=='compact'||!seen||seen.density!=='compact')throw new Error('setDensity did not apply, persist and announce');
if(setDensity('bogus')!=='default')throw new Error('invalid density must fall back to default');
// virtual list re-measures from --row-h on densitychange
const container={clientHeight:300,scrollTop:0,isConnected:true,addEventListener(){}};
let renders=0;
const vl=createDensityVirtualList({container,threshold:10,buffer:2,onScroll:()=>{renders++}});
rowh='30';
let w=vl.computeWindow(100);
if(w.topPad!==0||w.end!==Math.min(100,Math.ceil(300/30)+4))throw new Error('token height 30 not used: '+JSON.stringify(w));
rowh='44';container.scrollTop=440;
window.dispatchEvent(new CustomEvent('densitychange',{detail:{}}));
if(renders!==1)throw new Error('densitychange must re-render an active virtual list once, got '+renders);
w=vl.computeWindow(100);
if(w.topPad!==(Math.floor(440/44)-2)*44)throw new Error('row height not re-measured: '+JSON.stringify(w));
`
	cmd := exec.Command("node", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("core hooks/density under node: %v\n%s", err, out)
	}
}

func TestUIPrimitiveStylesheetsAnimateOnlyCompositorProperties(t *testing.T) {
	sheet := readUIAsset(t, "surfaces.css")
	at := strings.Index(sheet, "BottomSheet (sheet.js)")
	if at < 0 {
		t.Fatal("sheet styles missing from surfaces.css")
	}
	for name, body := range map[string]string{"primitives.css": readUIAsset(t, "primitives.css"), "surfaces.css (sheet)": sheet[at:]} {
		body = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(body, "")
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(body, -1) {
			for _, part := range strings.Split(m[1], ",") {
				f := strings.Fields(strings.TrimSpace(part))
				if len(f) > 0 && f[0] != "transform" && f[0] != "opacity" && f[0] != "none" {
					t.Errorf("%s transitions %q", name, f[0])
				}
			}
		}
	}
	app := readUIAsset(t, "app.css")
	requireUIRegex(t, mediaBlock(t, app, "@media (prefers-reduced-motion:reduce)"), `animation:none!important`)
	requireUIRegex(t, mediaBlock(t, app, "@media (prefers-reduced-motion:reduce)"), `transition:none!important`)
}

func TestUIPrimitiveTargetsAndFocusRings(t *testing.T) {
	css := readUIAsset(t, "primitives.css") + readUIAsset(t, "surfaces.css")
	for _, sel := range []string{".split-sash:focus-visible", ".sheet-handle:focus-visible", ".finder-input:focus-visible", ".diff-row:focus-visible", ".state-panel-title:focus-visible"} {
		if !strings.Contains(css, sel) {
			t.Errorf("%s has no visible focus style", sel)
		}
	}
	requireUIContains(t, css, "@media (pointer:coarse)", "min-height:44px")
}
