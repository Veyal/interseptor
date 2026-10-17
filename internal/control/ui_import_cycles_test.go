package control

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// staticImportRE matches top-level static ES module imports of a sibling module
// (`import {x} from './a.js'`, `import './a.js'`). Dynamic import() is lazy and
// cannot form an evaluation-order cycle, so it is ignored.
var staticImportRE = regexp.MustCompile(`(?m)^import\s+(?:[^'";]*?\s+from\s+)?['"]\./([A-Za-z0-9_.-]+\.js)['"]`)

func uiStaticImportGraph(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := fs.ReadDir(uiFS, "ui/js")
	if err != nil {
		t.Fatal(err)
	}
	graph := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".js" {
			continue
		}
		src := readUIAsset(t, "js/"+e.Name())
		seen := map[string]bool{}
		for _, m := range staticImportRE.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				graph[e.Name()] = append(graph[e.Name()], m[1])
			}
		}
		if _, ok := graph[e.Name()]; !ok {
			graph[e.Name()] = nil
		}
	}
	return graph
}

// uiImportCycles returns every strongly connected component with more than
// one module, each sorted, as "a,b,c" keys.
func uiImportCycles(graph map[string][]string) []string {
	index, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out []string
	n := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = n, n
		n++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range graph[v] {
			if _, ok := index[w]; !ok {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				sort.Strings(comp)
				out = append(out, strings.Join(comp, ","))
			}
		}
	}
	names := make([]string, 0, len(graph))
	for k := range graph {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, v := range names {
		if _, ok := index[v]; !ok {
			visit(v)
		}
	}
	sort.Strings(out)
	return out
}

// knownImportCycles are pre-existing cycles still to be broken. The list may
// only shrink: a new cycle (or a known one growing) fails the test.
var knownImportCycles = map[string]bool{
	"collections-editor.js,collections-response.js,collections-sheets.js,collections-tree.js": true,
}

// TestUINoNewStaticImportCycles: static ESM cycles risk TDZ errors at boot
// ("Cannot access X before initialization"); modules coordinate through bus.js
// or core.js hooks instead of importing each other.
func TestUINoNewStaticImportCycles(t *testing.T) {
	graph := uiStaticImportGraph(t)
	if len(graph) < 50 || len(graph["app.js"]) < 10 {
		t.Fatalf("import graph looks wrong: %d modules, app.js imports %v", len(graph), graph["app.js"])
	}
	for _, c := range uiImportCycles(graph) {
		if !knownImportCycles[c] {
			t.Errorf("static import cycle: %s (route the back-edge through bus.js EVENTS or a core.js hook)", c)
		}
	}
}

func TestUIImportCycleDetector(t *testing.T) {
	got := uiImportCycles(map[string][]string{
		"a.js": {"b.js"}, "b.js": {"a.js", "c.js"}, "c.js": nil,
		"d.js": {"e.js"}, "e.js": {"f.js"}, "f.js": {"d.js"},
	})
	if strings.Join(got, "|") != "a.js,b.js|d.js,e.js,f.js" {
		t.Fatalf("cycles = %v", got)
	}
}
