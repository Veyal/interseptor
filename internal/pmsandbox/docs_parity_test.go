package pmsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	parityBegin = "<!-- pm-parity:begin (generated from internal/pmsandbox/pm_coverage.json; run UPDATE_DOCS=1 go test ./internal/pmsandbox -run TestDocsParityTable) -->"
	parityEnd   = "<!-- pm-parity:end -->"
)

func sortedNoteKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func codeList(l []string) string {
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = "`" + s + "`"
	}
	return strings.Join(parts, ", ")
}

// renderParityTable builds the docs table from the embedded coverage data, the
// same data the static analyser and TestCoverageAgreesWithShim check against
// the running shim, so the page cannot drift from the runtime.
func renderParityTable(c *Coverage) string {
	var b strings.Builder
	b.WriteString("| Object | Supported members |\n| --- | --- |\n")
	names := make([]string, 0, len(c.Objects))
	for n := range c.Objects {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		o := c.Objects[n]
		switch {
		case len(o.Members) > 0:
			fmt.Fprintf(&b, "| `%s` | %s |\n", n, codeList(o.Members))
		case o.Chain != "":
			fmt.Fprintf(&b, "| `%s` | assertion chain: %s |\n", n, codeList(c.ResponseChain))
		}
	}
	fmt.Fprintf(&b, "\nChai `pm.expect()` words: %s.\n", codeList(c.Chai))
	fmt.Fprintf(&b, "\nGlobals: %s.\n", codeList(c.Globals.Supported))
	fmt.Fprintf(&b, "\n`require()` modules: %s.\n", codeList(c.Modules.Supported))
	b.WriteString("\n| Not shipped | Result |\n| --- | --- |\n")
	row := func(kind string, m map[string]string) {
		for _, k := range sortedNoteKeys(m) {
			fmt.Fprintf(&b, "| `%s` (%s) | `unsupported`: %s |\n", k, kind, m[k])
		}
	}
	row("pm API", c.Unsupported)
	row("global", c.Globals.Unsupported)
	row("module, deferred", c.Modules.Deferred)
	row("module, never", c.Modules.Forbidden)
	row("stub", c.Stubs)
	return b.String()
}

func TestDocsParityTable(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "collections.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i, j := strings.Index(doc, parityBegin), strings.Index(doc, parityEnd)
	if i < 0 || j < i {
		t.Fatalf("docs/collections.md lacks the %q ... %q markers", parityBegin, parityEnd)
	}
	want := doc[:i+len(parityBegin)] + "\n\n" + renderParityTable(LoadCoverage()) + "\n" + doc[j:]
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if want != doc {
		t.Fatal("docs/collections.md pm.* parity table is stale; run UPDATE_DOCS=1 go test ./internal/pmsandbox -run TestDocsParityTable")
	}
}
