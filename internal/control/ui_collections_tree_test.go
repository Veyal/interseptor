package control

import (
	"io/fs"
	"strings"
	"testing"
)

// The collection tree row must identify its request: method, name and path,
// with an accessible name and a non-colour cue for unresolved variables. Static
// assertions against the embedded assets; the browser probe lives in release
// evidence.
func TestCollectionsTreeRowIdentifiesItsRequest(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(uiFS, "ui/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	tree, css := read("js/collections-tree.js"), read("css/collections.css")

	for _, want := range []string{
		"M.rowLine(",            // path (and host when it differs) under the name
		"M.rowLabel(",           // accessible name: method + name + path
		"V.unresolvedIn(",       // reuse the variable model, no second parser
		"'coll-unset', 'unset'", // unresolved is text, never colour alone
		"row.setAttribute('role', 'treeitem')",
		"'aria-posinset'", "'aria-setsize'", "'aria-expanded'", "'aria-level'",
	} {
		if !strings.Contains(tree, want) {
			t.Errorf("collections-tree.js lost %q", want)
		}
	}
	for _, banned := range []string{".outerHTML =", "insertAdjacentHTML"} {
		if strings.Contains(tree, banned) {
			t.Errorf("collections-tree.js must build nodes, found %q", banned)
		}
	}
	for _, want := range []string{
		".coll-path .coll-head{flex:0 100 auto;min-width:0;overflow:hidden;text-overflow:ellipsis}", // head gives way first
		".coll-path .coll-tail{flex:0 1 auto;",                                                      // tail shrinks only once the head is gone
		".coll-unset{",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("collections.css lost %q", want)
		}
	}
}
