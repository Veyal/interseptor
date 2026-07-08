//go:build examples

// Test harness (build tag `examples`) that compiles the shipped example
// active-check scripts so a docs/example change can't silently ship a broken
// .star. Run with: go test -tags examples ./examples/active-checks
package activechecks_examples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/activescript"
)

func TestExampleActiveChecksCompile(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".star") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".star")
		src, err := os.ReadFile(filepath.Join(wd, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if _, err := activescript.Compile(id, string(src)); err != nil {
			t.Errorf("example %s failed to compile: %v", e.Name(), err)
		}
	}
}
