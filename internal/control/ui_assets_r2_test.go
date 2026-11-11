package control

import (
	"io/fs"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestJSStaticImportsScanner(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"default import", `import a from './a.js';`, []string{"./a.js"}},
		{"double quotes", `import a from "./a.js";`, []string{"./a.js"}},
		{"named import", `import { a, b as c } from './a.js';`, []string{"./a.js"}},
		{"namespace import", `import * as ns from './a.js';`, []string{"./a.js"}},
		{"default and named", `import d, { a } from './a.js';`, []string{"./a.js"}},
		{"default and namespace", `import d, * as ns from './a.js';`, []string{"./a.js"}},
		{"side effect import", `import './a.js';`, []string{"./a.js"}},
		{"no spaces", `import{a}from'./a.js';import'./b.js';`, []string{"./a.js", "./b.js"}},
		{"no semicolons", "import a from './a.js'\nimport b from './b.js'\nconst x = 1", []string{"./a.js", "./b.js"}},
		{"multi-line list", "import {\n  a,\n  b, // why b\n  c as d,\n} from './a.js';", []string{"./a.js"}},
		{"parent path", `import { a } from '../lib/a.js';`, []string{"../lib/a.js"}},
		{"re-export all", `export * from './a.js';`, []string{"./a.js"}},
		{"re-export namespace", `export * as ns from './a.js';`, []string{"./a.js"}},
		{"re-export names", "export {\n  a,\n  b as default,\n} from './a.js';", []string{"./a.js"}},
		{"string-named binding", `import { 'a-b' as ab } from './a.js'; export { ab as 'from' } from './b.js';`, []string{"./a.js", "./b.js"}},
		{"binding called from", `import from from './a.js'; import { from as f } from './b.js';`, []string{"./a.js", "./b.js"}},
		{"import attributes", `import data from './a.js' with { type: 'json' };`, []string{"./a.js"}},
		{"source order kept", "import './b.js';\nexport * from './a.js';\nimport c from './c.js';", []string{"./b.js", "./a.js", "./c.js"}},

		{"local export list", `const a = 1; export { a }; const from = './no.js';`, nil},
		{"local exports", "export const a = 1;\nexport function f() {}\nexport default class {}\nexport async function g() {}", nil},
		{"dynamic import", `const m = await import('./a.js'); import('./b.js').then(x => x);`, nil},
		{"dynamic import with space", `import ('./a.js');`, nil},
		{"dynamic import of a variable", `load(path => import(path));`, nil},
		{"import.meta", `const u = new URL('../css/a.css', import.meta.url).href;`, nil},
		{"property named import", `const o = { import: () => go() }; o.import('./a.js'); x.export = 1;`, nil},
		{"line comment", "// import a from './a.js';\nconst x = 1;", nil},
		{"block comment", "/* import a from './a.js';\n   export * from './b.js'; */\nconst x = 1;", nil},
		{"comment inside a list", "import { a /* from './no.js' */ } from './a.js';", []string{"./a.js"}},
		{"single-quoted string", `const s = 'import a from "./a.js"';`, nil},
		{"double-quoted string", `const s = "import a from './a.js'";`, nil},
		{"escaped quote in string", `const s = 'it\'s import x from "./a.js"'; import './b.js';`, []string{"./b.js"}},
		{"template literal", "const s = `\nimport a from './a.js';\n`;", nil},
		{"template substitution", "const s = `${ f(`import './a.js'`, '}') } import b from './b.js'`; import './c.js';", []string{"./c.js"}},
		{"go source in a string array", `const lines = ['import (', '\t"fmt"', '\t"io"'];`, nil},
		{"regular expression", `const re = /import x from '.\/a.js'/; const q = /['"]/g; import './b.js';`, []string{"./b.js"}},
		{"regex character class with slash", `const re = /[/'"]import/; import './b.js';`, []string{"./b.js"}},
		{"division is not a regex", `const r = a / b; import './a.js'; const s = c / d;`, []string{"./a.js"}},
		{"unterminated string stops at the line", "const s = 'oops\nimport './a.js';", []string{"./a.js"}},
		{"non-relative specifiers are still reported", `import a from 'pkg'; import b from '/js/b.js';`, []string{"pkg", "/js/b.js"}},
		{"empty", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsStaticImports([]byte(tc.src))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("jsStaticImports(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestResolveUIImport(t *testing.T) {
	cases := []struct {
		from, spec, want string
		ok               bool
	}{
		{"js/app.js", "./core.js", "js/core.js", true},
		{"js/app.js", "./sub/x.js", "js/sub/x.js", true},
		{"js/sub/x.js", "../core.js", "js/core.js", true},
		{"js/sub/deep/x.js", "../../core.js", "js/core.js", true},
		{"js/app.js", "../other/x.js", "other/x.js", true},
		{"js/app.js", "../../escape.js", "", false}, // above the embedded root
		{"js/app.js", "pkg", "", false},
		{"js/app.js", "/js/core.js", "", false}, // a second URL for the file
		{"js/app.js", "./core.js?v=1", "", false},
		{"js/app.js", "./data.json", "", false},
		{"js/app.js", "https://example.com/x.js", "", false},
	}
	for _, tc := range cases {
		got, ok := resolveUIImport(tc.from, tc.spec)
		if got != tc.want || ok != tc.ok {
			t.Errorf("resolveUIImport(%q, %q) = %q, %v; want %q, %v", tc.from, tc.spec, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUIStaticModuleGraphFollowsOnlyStaticEdges(t *testing.T) {
	files := map[string]string{
		"js/app.js":      "import { a } from './a.js';\nimport './shared.js';\nconst lazy = () => import('./lazy.js');",
		"js/second.js":   "export * from './lib/b.js';\nimport './shared.js';",
		"js/a.js":        "import './shared.js';\nimport './app.js'; // cycle\nimport './missing.js';",
		"js/shared.js":   "export const s = 1;",
		"js/lib/b.js":    "import { s } from '../shared.js';\nimport './c.js';",
		"js/lib/c.js":    "export const c = 1;",
		"js/lazy.js":     "import './lazy-dep.js';",
		"js/lazy-dep.js": "",
	}
	read := func(name string) ([]byte, bool) { s, ok := files[name]; return []byte(s), ok }
	got := uiStaticModuleGraph([]string{"js/app.js", "js/second.js"}, read)
	want := []string{"js/app.js", "js/second.js", "js/a.js", "js/shared.js", "js/lib/b.js", "js/lib/c.js"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("graph = %q, want %q (breadth first, each once, no lazy or missing modules)", got, want)
	}
}

func TestPreloadUIShellAddsMissingHintsOnce(t *testing.T) {
	files := map[string]string{
		"js/app.js":  "import './a.js'; import './b.js'; import('./lazy.js');",
		"js/a.js":    "import './b.js';",
		"js/b.js":    "",
		"js/lazy.js": "",
	}
	read := func(name string) ([]byte, bool) { s, ok := files[name]; return []byte(s), ok }
	index := "<head>\n<link rel=\"modulepreload\" href=\"/js/a.js\">\n<script>var x;</script>\n</head><body>\n<script type=\"module\" src=\"/js/app.js\"></script>\n</body>"
	got := string(preloadUIShell([]byte(index), read))
	want := "<head>\n<link rel=\"modulepreload\" href=\"/js/a.js\">\n<link rel=\"modulepreload\" href=\"/js/app.js\">\n<link rel=\"modulepreload\" href=\"/js/b.js\">\n<script>var x;</script>\n</head><body>\n<script type=\"module\" src=\"/js/app.js\"></script>\n</body>"
	if got != want {
		t.Fatalf("preloadUIShell =\n%s\nwant\n%s", got, want)
	}
	if again := string(preloadUIShell([]byte(got), read)); again != got {
		t.Fatalf("a fully hinted shell must be left alone, got\n%s", again)
	}
	// With no hints to extend, the new ones go just before the first module script.
	bare := "<body>\n<script type=\"module\" src=\"/js/app.js\"></script>\n</body>"
	got = string(preloadUIShell([]byte(bare), read))
	want = "<body>\n<link rel=\"modulepreload\" href=\"/js/app.js\">\n<link rel=\"modulepreload\" href=\"/js/a.js\">\n<link rel=\"modulepreload\" href=\"/js/b.js\">\n<script type=\"module\" src=\"/js/app.js\"></script>\n</body>"
	if got != want {
		t.Fatalf("preloadUIShell without existing hints =\n%s\nwant\n%s", got, want)
	}
	// Modules only a later module script needs are hinted after the entry's
	// graph and at low priority; one the entry also reaches is not repeated.
	files["js/second.js"] = "import './b.js'; import './only-second.js';"
	files["js/only-second.js"] = ""
	two := "<body>\n<script type=\"module\" src=\"/js/app.js\"></script>\n<script type=\"module\" src=\"/js/second.js\"></script>\n</body>"
	got = string(preloadUIShell([]byte(two), read))
	want = "<body>\n<link rel=\"modulepreload\" href=\"/js/app.js\">\n<link rel=\"modulepreload\" href=\"/js/a.js\">\n<link rel=\"modulepreload\" href=\"/js/b.js\">\n" +
		"<link rel=\"modulepreload\" href=\"/js/second.js\" fetchpriority=\"low\">\n<link rel=\"modulepreload\" href=\"/js/only-second.js\" fetchpriority=\"low\">\n" +
		"<script type=\"module\" src=\"/js/app.js\"></script>\n<script type=\"module\" src=\"/js/second.js\"></script>\n</body>"
	if got != want {
		t.Fatalf("preloadUIShell with two module scripts =\n%s\nwant\n%s", got, want)
	}
	if plain := "<body><script src=\"/js/classic.js\"></script></body>"; string(preloadUIShell([]byte(plain), read)) != plain {
		t.Fatal("a shell with no module script must be returned unchanged")
	}
}

// naiveStaticImports is an independent, line-oriented reading of the same
// declarations. It is only right for the way the embedded modules are written
// (declarations start a line, no declaration-like text inside strings); the
// scanner must agree with it on every embedded module.
var naiveImportDecl = regexp.MustCompile(`(?m)^(?:import|export)\b(?:[^;'"` + "`" + `(]*?\bfrom)?\s*['"]([^'"]+)['"]`)

func naiveStaticImports(src string) []string {
	var out []string
	for _, m := range naiveImportDecl.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

func embeddedUIModules(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(uiFS, "ui/js", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".js") {
			names = append(names, strings.TrimPrefix(p, "ui/"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestJSStaticImportsAgreesWithLineReadingOnEmbeddedModules(t *testing.T) {
	names := embeddedUIModules(t)
	if len(names) < 100 {
		t.Fatalf("only %d embedded modules found", len(names))
	}
	edges := 0
	for _, name := range names {
		src := readUIAsset(t, name)
		got, want := jsStaticImports([]byte(src)), naiveStaticImports(src)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: scanner found %q, line reading found %q", name, got, want)
		}
		edges += len(got)
	}
	if edges < 300 {
		t.Fatalf("only %d static import edges in the embedded UI; the scanner is missing declarations", edges)
	}
}

var servedPreloadHint = regexp.MustCompile(`<link rel="modulepreload" href="([^"]+)"( fetchpriority="low")?>`)

// servedLowPriorityHints returns the embedded names hinted with fetchpriority="low".
func servedLowPriorityHints(body string) map[string]bool {
	low := map[string]bool{}
	for _, m := range servedPreloadHint.FindAllStringSubmatch(body, -1) {
		if m[2] != "" {
			low[m[1][strings.Index(m[1], "/js/")+1:]] = true
		}
	}
	return low
}

func servedShellHints(t *testing.T) (body string, hints []string) {
	t.Helper()
	root := uiGet(t, "/", nil)
	if root.Code != http.StatusOK {
		t.Fatalf("index status = %d", root.Code)
	}
	body = root.Body.String()
	for _, m := range servedPreloadHint.FindAllStringSubmatch(body, -1) {
		hints = append(hints, m[1])
	}
	return body, hints
}

// The served shell hints exactly the static import graph of its module
// scripts: every module a boot is certain to evaluate, and nothing that is
// only loaded on demand.
func TestServedShellPreloadsTheWholeStaticModuleGraph(t *testing.T) {
	body, hints := servedShellHints(t)
	prefix := "/" + uiVersionPrefix + uiBuildID() + "/"

	index := readUIAsset(t, "index.html")
	var roots []string
	for _, m := range regexp.MustCompile(`<script type="module" src="/([^"]+)">`).FindAllStringSubmatch(index, -1) {
		roots = append(roots, m[1])
	}
	if len(roots) < 2 {
		t.Fatalf("index.html module scripts = %q", roots)
	}
	// The expected graph is built with the independent line reading, not the
	// scanner under test.
	want := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if want[name] {
			continue
		}
		want[name] = true
		for _, spec := range naiveStaticImports(readUIAsset(t, name)) {
			if dep, ok := resolveUIImport(name, spec); ok {
				queue = append(queue, dep)
			}
		}
	}

	got := map[string]bool{}
	for _, href := range hints {
		name, ok := strings.CutPrefix(href, prefix)
		if !ok {
			t.Errorf("hint %q is not under the build prefix %q", href, prefix)
			continue
		}
		if got[name] {
			t.Errorf("module %s is hinted more than once", name)
		}
		got[name] = true
		if _, err := fs.Stat(uiFS, "ui/"+name); err != nil {
			t.Errorf("hint names %s, which is not an embedded file", name)
		}
	}
	var missing, extra []string
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	for name := range got {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("hinted set differs from the static graph (%d modules): missing %q, extra %q", len(want), missing, extra)
	}
	if len(got) < 100 {
		t.Fatalf("only %d modules hinted; the static graph of the entry points is larger", len(got))
	}

	// The application entry's own graph is hinted at normal priority; only the
	// modules the later module scripts add are marked low.
	entry := map[string]bool{}
	for _, name := range uiStaticModuleGraph(roots[:1], readEmbeddedUIModule) {
		entry[name] = true
	}
	low := servedLowPriorityHints(body)
	for name := range got {
		if low[name] == entry[name] {
			t.Errorf("%s: low priority = %v, in the entry graph = %v", name, low[name], entry[name])
		}
	}
	if len(low) == 0 || len(low) >= len(got)/2 {
		t.Fatalf("%d of %d hints are low priority; want only the later scripts' own modules", len(low), len(got))
	}

	// Every hint precedes the first module script, so the fetches start before
	// the first import wave would have discovered them.
	firstScript := strings.Index(body, `<script type="module"`)
	lastHint := strings.LastIndex(body, `<link rel="modulepreload"`)
	if firstScript < 0 || lastHint < 0 || lastHint > firstScript {
		t.Fatalf("hints must precede the first module script (last hint at %d, first script at %d)", lastHint, firstScript)
	}
	// The hints written in index.html are kept, in place, exactly once.
	for _, mod := range []string{"core.js", "bus.js", "proxy.js", "intercept.js", "settings.js", "findings.js", "tools.js"} {
		if n := strings.Count(body, `<link rel="modulepreload" href="`+prefix+`js/`+mod+`">`); n != 1 {
			t.Errorf("js/%s hinted %d times in the served shell, want 1", mod, n)
		}
	}
}

func TestServedShellDoesNotPreloadLazyModules(t *testing.T) {
	_, hints := servedShellHints(t)
	hinted := map[string]bool{}
	for _, href := range hints {
		hinted[href[strings.LastIndex(href, "/")+1:]] = true
	}
	if len(hinted) < 100 {
		t.Fatalf("only %d hints parsed from the served shell", len(hinted))
	}
	// map.js is loaded by project.js on demand, apipanel.js by app.js when
	// its panel opens, flowdrawer.js after the workspace is ready.
	for _, lazy := range []string{"map.js", "map-model.js", "apipanel.js", "flowdrawer.js"} {
		if !strings.Contains(readUIAsset(t, "js/"+lazy), "export") {
			t.Fatalf("js/%s is not the module this test assumes", lazy)
		}
		if hinted[lazy] {
			t.Errorf("%s is only loaded dynamically and must not be hinted", lazy)
		}
	}
	// The optional modules load after the workspace is ready. One is hinted
	// only when the static graph reaches it too.
	static := map[string]bool{}
	var roots []string
	for _, m := range uiShellModuleScript.FindAllStringSubmatch(readUIAsset(t, "index.html"), -1) {
		roots = append(roots, m[1])
	}
	for _, name := range uiStaticModuleGraph(roots, readEmbeddedUIModule) {
		static[strings.TrimPrefix(name, "js/")] = true
	}
	list := regexp.MustCompile(`(?s)export const OPTIONAL_MODULES = \[(.*?)\]`).FindStringSubmatch(readUIAsset(t, "js/shell-hooks.js"))
	if list == nil {
		t.Fatal("OPTIONAL_MODULES not found in shell-hooks.js")
	}
	names := regexp.MustCompile(`'([a-z-]+)'`).FindAllStringSubmatch(list[1], -1)
	if len(names) < 5 {
		t.Fatalf("OPTIONAL_MODULES = %q", list[1])
	}
	lazyOnly := 0
	for _, m := range names {
		file := m[1] + ".js"
		if hinted[file] != static[file] {
			t.Errorf("optional module %s: hinted=%v, statically reached=%v", file, hinted[file], static[file])
		}
		if !static[file] {
			lazyOnly++
		}
	}
	t.Logf("%d hints; %d of %d optional modules are lazy-only and unhinted", len(hints), lazyOnly, len(names))
}

// index.html on disk keeps its seven root hints (source-text tests assert on
// them); the rest are added when the shell is served, and the shell's
// validator is the digest of what is served.
func TestServedShellETagCoversInjectedHints(t *testing.T) {
	disk := readUIAsset(t, "index.html")
	if n := strings.Count(disk, `rel="modulepreload"`); n != 7 {
		t.Fatalf("index.html on disk has %d modulepreload hints, want the 7 root hints", n)
	}
	body, hints := servedShellHints(t)
	if len(hints) <= 7 {
		t.Fatalf("served shell has %d hints, want the injected graph", len(hints))
	}
	shell := uiShell()
	if string(shell.data) != body {
		t.Fatal("the served body is not the prepared shell")
	}
	if want := newUIAsset("index.html", []byte(body)).etag; shell.etag != want {
		t.Fatalf("shell ETag %s does not cover the served markup (want %s)", shell.etag, want)
	}
	if plain := newUIAsset("index.html", versionUIShell([]byte(disk), uiBuildID())).etag; shell.etag == plain {
		t.Fatal("shell ETag equals the digest of the shell without the injected hints")
	}
	rec := uiGet(t, "/", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("ETag") != shell.gzETag {
		t.Fatalf("gzip shell: encoding %q etag %q, want gzip and %s", rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"), shell.gzETag)
	}
}

// project.js reads /api/project and /api/version before the shell evaluates.
// The shell's first loaders take those answers instead of asking again, but an
// answer must never outlive a change: a mutation drops it.
func TestUIBootReadsAreDroppedByAMutation(t *testing.T) {
	// clearSharedReads is the one-line function live events call (app.js).
	line := regexp.MustCompile(`(?m)^export (function clearSharedReads\(prefix\)\{.*\})$`).FindStringSubmatch(readUIAsset(t, "js/core.js"))
	if line == nil {
		t.Fatal("clearSharedReads not found in core.js")
	}
	shellFeedbackHarness(t, line[1]+`
globalThis.location={pathname:'/',href:''};
AbortSignal.timeout??=()=>({});
let calls=[];
globalThis.fetch=async(p,o)=>{calls.push((o&&o.method||'GET')+' '+p);return {ok:true,status:200,headers:{get:()=>'application/json'},text:async()=>'{"n":1}',json:async()=>({n:1})};};
bootReads.keep('/api/project',Promise.resolve({current:'boot'}));
bootReads.keep('/api/version',Promise.resolve({project:'boot'}));
eq((await bootReads.take('/api/project')).current,'boot','the first load takes the boot answer');
eq(bootReads.take('/api/project'),null,'a boot answer is handed out once');
await api('/api/ui/repeater',{method:'PUT',body:'{}'});
eq(bootReads.size(),1,'a workspace autosave changes nothing the boot reads describe');
await api('/api/settings',{method:'PUT',body:'{}'});
eq(bootReads.take('/api/version'),null,'a read issued after a mutation must not get the boot answer');
bootReads.keep('/api/version',Promise.resolve({project:'boot'}));
clearSharedReads();
eq(bootReads.take('/api/version'),null,'a live event drops the boot answers with the shared reads');
bootReads.keep('/api/version',Promise.resolve({project:'boot'}));
clearSharedReads('/api/ui/');
eq(bootReads.size(),1,'a scoped clear leaves unrelated boot answers alone');
`)
}

func TestUIBootLoadersTakeTheProjectIdentityReads(t *testing.T) {
	project := executableJS(readUIAsset(t, "js/project.js"))
	requireUIContains(t, project, "bootReads.keep(path,", "settleBootRead(value)", "settleBootRead(undefined)", "export function bootProjectSnapshot()")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, settings, "(await bootReads.take('/api/project'))||await api('/api/project')")
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "(await bootReads.take('/api/version'))||await api('/api/version')", "const hadError=sseHadError;", "await projectStorageReady.catch(()=>{});", "streamHelloProjectCheck({knownKey:sseProjectKey,")
	// The answers are released as soon as the boot loaders have been started.
	boot := strings.Index(app, "loadProject();loadVersion(true);")
	clear := strings.Index(app, "bootReads.clear();")
	if boot < 0 || clear < boot {
		t.Fatal("app.js must clear the boot reads right after starting the boot loaders")
	}
}
