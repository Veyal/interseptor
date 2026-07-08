package activescript_test

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/activescan"
	"github.com/Veyal/interseptor/internal/activescript"
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

func TestActiveXXETemplateUsesSafeCanaryNotFileRead(t *testing.T) {
	src, ok := activescan.BuiltinTemplate("active-xxe")
	if !ok {
		t.Fatal("missing active-xxe template")
	}
	if strings.Contains(src, `<!ENTITY`) && strings.Contains(src, "SYSTEM") {
		t.Fatal("active-xxe template must not use SYSTEM/file-read entities")
	}
	if !strings.Contains(src, "INTERSEPTOR_XXE_CANARY") {
		t.Fatal("active-xxe template must use the internal-entity canary")
	}
}
