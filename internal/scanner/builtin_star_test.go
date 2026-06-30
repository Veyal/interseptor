package scanner

import (
	"testing"

	"github.com/Veyal/interceptor/internal/checkscript"
)

func TestBuiltinTemplatesCompile(t *testing.T) {
	for _, b := range BuiltinChecks {
		src, ok := BuiltinTemplate(b.ID)
		if !ok {
			t.Fatalf("missing template for %q", b.ID)
		}
		if _, err := checkscript.Compile(b.ID, src); err != nil {
			t.Fatalf("compile %q: %v", b.ID, err)
		}
	}
}

func TestIsBuiltinID(t *testing.T) {
	if !IsBuiltinID(checkSecurityHeaders) {
		t.Fatal("security-headers should be builtin")
	}
	if IsBuiltinID("not-a-check") {
		t.Fatal("unknown id")
	}
}
