package collexec

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/redact"
)

func nameHeuristic(n string) bool { return strings.Contains(n, "token") }

// The first write of a brand-new secret-named variable must register the
// value for masking at once, before any commit declares it.
func TestVarsSetNewSecretNamedVariableIsMaskedImmediately(t *testing.T) {
	reg := redact.NewRegistry()
	v := newVars(nil, nil, reg, nameHeuristic)
	if err := v.Set(VarEnvironment, "fresh_token", "CANARY-RESPBODY"); err != nil {
		t.Fatal(err)
	}
	if got := reg.Mask("got CANARY-RESPBODY"); strings.Contains(got, "CANARY-RESPBODY") {
		t.Fatalf("value not masked: %q", got)
	}
	ch := v.Changes()
	if len(ch) != 1 || !ch[0].Secret || ch[0].Display != "[secret]" {
		t.Fatalf("change not marked secret: %+v", ch)
	}
	// ordinary names stay plain; no classifier keeps the old behaviour
	if err := v.Set(VarEnvironment, "page", "PLAINVALUE-12345"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reg.Mask("PLAINVALUE-12345"), "***") || reg.Mask("PLAINVALUE-12345") != "PLAINVALUE-12345" {
		t.Fatal("non-secret name was registered")
	}
	v2 := newVars(nil, nil, reg, nil)
	if err := v2.Set(VarEnvironment, "other_token", "NOCLASSIFIER-VALUE"); err != nil {
		t.Fatal(err)
	}
	if reg.Mask("NOCLASSIFIER-VALUE") != "NOCLASSIFIER-VALUE" {
		t.Fatal("nil classifier must not mask")
	}
}
