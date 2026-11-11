package collexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

const nestCanary = "CANARYnestedSecret4410"

// A nested reference ({{ x-{{tok}} }}) resolves the inner secret first and then
// fails to find the outer name. The name it reports is built from the secret
// value and must be masked everywhere in the result.
func TestNestedSecretNameIsMaskedInUnresolved(t *testing.T) {
	e := newEnv(t)
	in := StepInput{
		Chain:       chainOf(store.Item{Method: "GET", URL: js("https://example.com/{{pre-{{tok}}}}")}),
		Layers:      []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"tok": nestCanary}, "tok")},
		ScopePolicy: store.ScopePolicyOff,
	}
	res, err := e.pipe.Step(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockUnresolved {
		t.Fatalf("outcome %s/%s", res.Outcome, res.BlockReason)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), nestCanary) {
		t.Fatalf("secret value leaked through an unresolved nested name:\n%s", raw)
	}
}

// Unsetting a secret and writing it again in the same script (a token
// rotation, or environment.clear() followed by set) must keep it secret.
func TestSecretStaysSecretAfterUnsetThenSet(t *testing.T) {
	const rotated = "CANARYrotatedValue9912"
	v := newVars([]varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"sess": "old-value-000"}, "sess")}, nil, nil, nil)
	if err := v.Unset(VarEnvironment, "sess"); err != nil {
		t.Fatal(err)
	}
	if err := v.Set(VarEnvironment, "sess", rotated); err != nil {
		t.Fatal(err)
	}
	ch := v.Changes()
	if len(ch) != 1 || !ch[0].Secret || strings.Contains(ch[0].Display, rotated) {
		t.Fatalf("rotated secret lost its secret flag: %+v", ch)
	}
	if err := v.Clear(VarEnvironment); err != nil {
		t.Fatal(err)
	}
	_ = v.Set(VarEnvironment, "sess", rotated)
	raw, _ := json.Marshal(v.Changes())
	if strings.Contains(string(raw), rotated) {
		t.Fatalf("secret shown after clear+set: %s", raw)
	}
}
