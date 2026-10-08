package collexec

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/varstore"
)

// Script-facing variable scope names.
const (
	VarEnvironment = "environment"
	VarGlobals     = "globals"
	VarCollection  = "collection"
	VarLocal       = "local"
	VarData        = "data"
)

func scopeOf(name string) (varstore.Scope, bool) {
	switch name {
	case VarEnvironment:
		return varstore.ScopeEnvironment, true
	case VarGlobals:
		return varstore.ScopeGlobal, true
	case VarCollection:
		return varstore.ScopeCollection, true
	case VarLocal:
		return varstore.ScopeLocal, true
	case VarData:
		return varstore.ScopeData, true
	}
	return 0, false
}

// Vars is the live variable state of one Step. Scripts read and write it;
// writes are visible to later scripts and to resolution, and are recorded as
// VarChanges for the caller to commit per its persist policy. Safe for
// concurrent use.
type Vars struct {
	mu      sync.Mutex
	layers  []varstore.Layer
	changes []VarChange
	reg     *redact.Registry
	// secretName classifies a variable name the script writes for the first
	// time; nil keeps secret-ness to already-declared variables only.
	secretName func(string) bool
}

func newVars(base []varstore.Layer, local map[string]string, reg *redact.Registry, secretName func(string) bool) *Vars {
	v := &Vars{reg: reg, secretName: secretName}
	for _, l := range base {
		nl := varstore.Layer{Scope: l.Scope, Name: l.Name, Vars: make(map[string]varstore.Var, len(l.Vars))}
		for k, x := range l.Vars {
			nl.Vars[k] = x
		}
		v.layers = append(v.layers, nl)
	}
	if len(local) > 0 {
		l := varstore.Layer{Scope: varstore.ScopeLocal, Name: "local", Vars: map[string]varstore.Var{}}
		for k, x := range local {
			l.Vars[k] = varstore.Var{Value: x}
		}
		v.layers = append(v.layers, l)
	}
	return v
}

// Stack builds a resolver stack from the current state.
func (v *Vars) Stack() *varstore.Stack {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stackLocked()
}

func (v *Vars) stackLocked() *varstore.Stack {
	ls := make([]varstore.Layer, len(v.layers))
	copy(ls, v.layers)
	return varstore.NewStack(ls...)
}

// Get returns the winning value for name across all scopes.
func (v *Vars) Get(name string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	x, _, ok := v.stackLocked().Lookup(name)
	return x.Value, ok
}

// GetIn returns name from one scope only.
func (v *Vars) GetIn(scope, name string) (string, bool) {
	sc, ok := scopeOf(scope)
	if !ok {
		return "", false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, l := range v.layers {
		if l.Scope == sc {
			if x, ok := l.Vars[name]; ok {
				return x.Value, true
			}
		}
	}
	return "", false
}

// ToObject returns every variable in scope as a plain map.
func (v *Vars) ToObject(scope string) map[string]string {
	sc, ok := scopeOf(scope)
	out := map[string]string{}
	if !ok {
		return out
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	// Inner (earlier) layers win within a scope.
	for i := len(v.layers) - 1; i >= 0; i-- {
		if v.layers[i].Scope == sc {
			for k, x := range v.layers[i].Vars {
				out[k] = x.Value
			}
		}
	}
	return out
}

// Set writes name in scope. The data scope is read-only (Postman parity).
// A secret flag already on the variable is preserved and its value is added to
// the masking registry.
func (v *Vars) Set(scope, name, value string) error {
	sc, ok := scopeOf(scope)
	if !ok {
		return fmt.Errorf("collexec: unknown variable scope %q", scope)
	}
	if sc == varstore.ScopeData {
		return fmt.Errorf("collexec: iteration data is read-only")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	var target *varstore.Layer
	secret, existed := false, false
	for i := range v.layers {
		if v.layers[i].Scope != sc {
			continue
		}
		if target == nil {
			target = &v.layers[i]
		}
		if x, ok := v.layers[i].Vars[name]; ok {
			target, secret, existed = &v.layers[i], x.Secret, true
			break
		}
	}
	if target == nil {
		v.layers = append(v.layers, varstore.Layer{Scope: sc, Name: scope, Vars: map[string]varstore.Var{}})
		target = &v.layers[len(v.layers)-1]
	}
	if !existed && v.secretName != nil && v.secretName(name) {
		secret = true // a credential written for the first time is a secret from that moment
	}
	target.Vars[name] = varstore.Var{Value: value, Secret: secret}
	if secret {
		v.reg.Add(value)
	}
	ch := VarChange{Scope: scope, Key: name, Value: value, Secret: secret}
	ch.Display = value
	if secret {
		ch.Display = "[secret]"
	}
	v.changes = append(v.changes, ch)
	return nil
}

// Unset removes name from scope.
func (v *Vars) Unset(scope, name string) error {
	sc, ok := scopeOf(scope)
	if !ok || sc == varstore.ScopeData {
		return fmt.Errorf("collexec: cannot unset in scope %q", scope)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, l := range v.layers {
		if l.Scope == sc {
			delete(l.Vars, name)
		}
	}
	v.changes = append(v.changes, VarChange{Scope: scope, Key: name, Unset: true})
	return nil
}

// Clear removes every variable in scope.
func (v *Vars) Clear(scope string) error {
	for k := range v.ToObject(scope) {
		if err := v.Unset(scope, k); err != nil {
			return err
		}
	}
	return nil
}

// Changes returns the recorded writes in order, collapsed to the last write
// per (scope,key).
func (v *Vars) Changes() []VarChange {
	v.mu.Lock()
	defer v.mu.Unlock()
	last := map[string]int{}
	for i, c := range v.changes {
		last[c.Scope+"\x00"+c.Key] = i
	}
	var idx []int
	for _, i := range last {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]VarChange, 0, len(idx))
	for _, i := range idx {
		out = append(out, v.changes[i])
	}
	return out
}
