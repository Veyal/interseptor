package varstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// A nested name that consumed a secret is reported as written. The resolver
// promises that results and errors never carry values, with or without a
// masking registry.
func TestUnresolvedNestedNameNeverCarriesSecretValue(t *testing.T) {
	const canary = "CANARYnested551"
	st := NewStack(Layer{Scope: ScopeEnvironment, Name: "e", Vars: map[string]Var{
		"tok": {Value: canary, Secret: true}, "plain": {Value: "abc"},
	}})
	for _, tpl := range []string{"{{pre-{{tok}}}}", "{{ {{tok}} }}", "{{x{{tok}}|b64}}", "{{ {{tok}}|nopipe }}", "{{$nope{{tok}}}}"} {
		res := New(Options{}).Resolve(tpl, st)
		raw, _ := json.Marshal(res)
		msg := ""
		if res.Err != nil {
			msg = res.Err.Error()
		}
		if strings.Contains(string(raw), canary) || strings.Contains(msg, canary) {
			t.Fatalf("%s leaked the secret: %s / %s", tpl, raw, msg)
		}
		if res.Err == nil || len(res.Unresolved) != 1 {
			t.Fatalf("%s: want one unresolved name and an error, got %s", tpl, raw)
		}
	}
	// A nested name with no secret inside still reports the expanded name:
	// that is the useful one when debugging.
	res := New(Options{}).Resolve("{{pre-{{plain}}}}", st)
	if len(res.Unresolved) != 1 || res.Unresolved[0] != "pre-abc" {
		t.Fatalf("plain nested name: %v", res.Unresolved)
	}
}
