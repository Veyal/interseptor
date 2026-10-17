package control

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Collections modules (tree, editor, response, sheets, env, …) used to
// import each other in a ring. Cross-module calls now go through the X
// registry in collections-core.js, so the static import graph between the
// collections*.js files must stay acyclic.
func TestCollectionsModulesHaveNoImportCycle(t *testing.T) {
	importRe := regexp.MustCompile(`(?m)^import\s[^;]*?from\s+'\./(collections[\w-]*\.js)'`)
	entries, err := fs.ReadDir(uiFS, "ui/js")
	if err != nil {
		t.Fatal(err)
	}
	graph := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "collections") || !strings.HasSuffix(name, ".js") {
			continue
		}
		src := readUIAsset(t, "js/"+name)
		for _, m := range importRe.FindAllStringSubmatch(src, -1) {
			graph[name] = append(graph[name], m[1])
		}
		if _, ok := graph[name]; !ok {
			graph[name] = nil
		}
	}
	if len(graph) < 5 {
		t.Fatalf("expected the collections modules, found %d", len(graph))
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var visit func(n string)
	visit = func(n string) {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range graph[n] {
			switch color[m] {
			case grey:
				i := len(stack) - 1
				for i > 0 && stack[i] != m {
					i--
				}
				t.Errorf("import cycle: %s -> %s", strings.Join(stack[i:], " -> "), m)
			case white:
				visit(m)
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	names := make([]string, 0, len(graph))
	for n := range graph {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if color[n] == white {
			visit(n)
		}
	}
}
