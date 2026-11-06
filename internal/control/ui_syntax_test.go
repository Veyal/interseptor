package control

import (
	"bytes"
	"os/exec"
	"path"
	"strings"
	"testing"
)

// Every embedded UI module must at least parse. A single unescaped quote in an
// added string takes the whole SPA down (the browser reports only "missing )
// after argument list" against the bundle's module graph, and every string
// assertion in the other UI tests still passes), so gate the module set here.
func TestUIJavaScriptModulesParse(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir, err := uiFS.ReadDir("ui/js")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range dir {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".js") {
			continue
		}
		checked++
		src, err := uiFS.ReadFile(path.Join("ui/js", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cmd := exec.Command("node", "--input-type=module", "--check")
		cmd.Stdin = bytes.NewReader(src)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("ui/js/%s does not parse as an ES module: %v\n%s", name, err, out)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d UI modules checked; the embedded module set looks wrong", checked)
	}
}
