package activescript_test

import (
	"testing"

	"github.com/Veyal/interceptor/internal/activescan"
	"github.com/Veyal/interceptor/internal/activescript"
)

func TestActiveBuiltinTemplatesCompile(t *testing.T) {
	for _, c := range activescan.Checks {
		src, ok := activescan.BuiltinTemplate(c.ID)
		if !ok {
			t.Fatalf("missing template for %q", c.ID)
		}
		if _, err := activescript.Compile(c.ID, src); err != nil {
			t.Fatalf("compile %q: %v", c.ID, err)
		}
	}
}
