package collrun

import (
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/varstore"
)

func baseLayers() []varstore.Layer {
	return []varstore.Layer{
		{Scope: varstore.ScopeEnvironment, Name: "env", Vars: map[string]varstore.Var{"token": {Value: "old"}, "gone": {Value: "x"}}},
	}
}

func TestOverlayApplyDoesNotMutateInput(t *testing.T) {
	base := baseLayers()
	o := NewOverlay()
	o.Record([]collexec.VarChange{
		{Scope: "environment", Key: "token", Value: "new"},
		{Scope: "environment", Key: "gone", Unset: true},
		{Scope: "collection", Key: "extra", Value: "e"},
	})
	got := o.Apply(base)
	if base[0].Vars["token"].Value != "old" || base[0].Vars["gone"].Value != "x" {
		t.Fatal("input layers were mutated: discard would not be exact")
	}
	st := varstore.NewStack(got...)
	if v, _, ok := st.Lookup("token"); !ok || v.Value != "new" {
		t.Fatalf("token: %+v %v", v, ok)
	}
	if _, _, ok := st.Lookup("gone"); ok {
		t.Fatal("unset var still visible")
	}
	if v, _, ok := st.Lookup("extra"); !ok || v.Value != "e" {
		t.Fatalf("new collection var missing: %+v", v)
	}
}

func TestOverlayPendingLastWriteWinsAndOrder(t *testing.T) {
	o := NewOverlay()
	o.Record([]collexec.VarChange{{Scope: "environment", Key: "a", Value: "1"}, {Scope: "environment", Key: "b", Value: "2"}})
	o.Record([]collexec.VarChange{{Scope: "environment", Key: "a", Value: "3", Secret: true, Display: "[secret]"}, {Scope: "environment", Key: "b", Unset: true}})
	p := o.Pending()
	if len(p) != 2 || p[0].Key != "a" || p[0].Value != "3" || !p[0].Secret || p[1].Key != "b" || !p[1].Unset {
		t.Fatalf("pending: %+v", p)
	}
}

func TestOverlayIgnoresLocalScope(t *testing.T) {
	o := NewOverlay()
	o.Record([]collexec.VarChange{{Scope: "local", Key: "x", Value: "1"}})
	if !o.Empty() {
		t.Fatal("local writes are per step and must not be recorded")
	}
}

func TestOverlaySecretFlagSticks(t *testing.T) {
	base := []varstore.Layer{{Scope: varstore.ScopeEnvironment, Vars: map[string]varstore.Var{"k": {Value: "v", Secret: true}}}}
	o := NewOverlay()
	o.Record([]collexec.VarChange{{Scope: "environment", Key: "k", Value: "new-secret-value"}})
	v, _, _ := varstore.NewStack(o.Apply(base)...).Lookup("k")
	if !v.Secret || v.Value != "new-secret-value" {
		t.Fatalf("secret flag lost: %+v", v)
	}
}
