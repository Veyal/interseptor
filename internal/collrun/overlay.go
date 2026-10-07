package collrun

import (
	"sort"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/varstore"
)

// overlayScopes are the script-writable scopes that survive between steps.
var overlayScopes = map[string]varstore.Scope{
	collexec.VarEnvironment: varstore.ScopeEnvironment,
	collexec.VarGlobals:     varstore.ScopeGlobal,
	collexec.VarCollection:  varstore.ScopeCollection,
}

type overlayVal struct {
	value   string
	display string
	secret  bool
}

// Overlay is the copy-on-write variable state of one run. Script writes land
// here, never in the store, so a run with persist=discard is exact: the stored
// variables are untouched and later steps still see earlier writes (token
// chains). Layers handed to apply are copied, never mutated.
type Overlay struct {
	set   map[string]map[string]overlayVal
	unset map[string]map[string]bool
	order []string // scope\x00key in first-write order, for stable output
}

// NewOverlay returns an empty overlay.
func NewOverlay() *Overlay {
	return &Overlay{set: map[string]map[string]overlayVal{}, unset: map[string]map[string]bool{}}
}

func (o *Overlay) touch(scope, key string) {
	id := scope + "\x00" + key
	for _, e := range o.order {
		if e == id {
			return
		}
	}
	o.order = append(o.order, id)
}

// Record folds a step's variable changes into the overlay. Local-scope writes
// are per step and never recorded.
func (o *Overlay) Record(changes []collexec.VarChange) {
	for _, ch := range changes {
		if _, ok := overlayScopes[ch.Scope]; !ok {
			continue
		}
		if o.set[ch.Scope] == nil {
			o.set[ch.Scope], o.unset[ch.Scope] = map[string]overlayVal{}, map[string]bool{}
		}
		o.touch(ch.Scope, ch.Key)
		if ch.Unset {
			delete(o.set[ch.Scope], ch.Key)
			o.unset[ch.Scope][ch.Key] = true
			continue
		}
		o.set[ch.Scope][ch.Key] = overlayVal{value: ch.Value, display: ch.Display, secret: ch.Secret}
		delete(o.unset[ch.Scope], ch.Key)
	}
}

// Empty reports whether nothing was written.
func (o *Overlay) Empty() bool { return len(o.order) == 0 }

// Pending lists the net writes (last write per scope+key, unsets included) in
// first-write order. Values are raw: hand them only to a Backend.Commit.
func (o *Overlay) Pending() []collexec.VarChange {
	var out []collexec.VarChange
	for _, id := range o.order {
		scope, key := splitID(id)
		if v, ok := o.set[scope][key]; ok {
			out = append(out, collexec.VarChange{Scope: scope, Key: key, Value: v.value, Display: v.display, Secret: v.secret})
		} else if o.unset[scope][key] {
			out = append(out, collexec.VarChange{Scope: scope, Key: key, Unset: true})
		}
	}
	return out
}

func splitID(id string) (scope, key string) {
	for i := 0; i < len(id); i++ {
		if id[i] == 0 {
			return id[:i], id[i+1:]
		}
	}
	return id, ""
}

// Apply returns copies of layers with the overlay on top. The input is never
// modified.
func (o *Overlay) Apply(layers []varstore.Layer) []varstore.Layer {
	out := make([]varstore.Layer, len(layers))
	for i, l := range layers {
		vars := make(map[string]varstore.Var, len(l.Vars))
		for k, v := range l.Vars {
			vars[k] = v
		}
		out[i] = varstore.Layer{Scope: l.Scope, Name: l.Name, Vars: vars}
	}
	names := make([]string, 0, len(overlayScopes))
	for n := range overlayScopes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := overlayScopes[name]
		for k := range o.unset[name] {
			for i := range out {
				if out[i].Scope == sc {
					delete(out[i].Vars, k)
				}
			}
		}
		for k, v := range o.set[name] {
			out = setInLayers(out, sc, k, v)
		}
	}
	return out
}

func setInLayers(layers []varstore.Layer, sc varstore.Scope, key string, v overlayVal) []varstore.Layer {
	for i := range layers {
		if layers[i].Scope == sc {
			cur := layers[i].Vars[key]
			cur.Value = v.value
			cur.Secret = cur.Secret || v.secret
			layers[i].Vars[key] = cur
			return layers
		}
	}
	return append(layers, varstore.Layer{Scope: sc, Name: sc.String(), Vars: map[string]varstore.Var{key: {Value: v.value, Secret: v.secret}}})
}

// dataLayer builds the iteration-data layer for one row.
func dataLayer(row map[string]string) varstore.Layer {
	vars := make(map[string]varstore.Var, len(row))
	for k, v := range row {
		vars[k] = varstore.Var{Value: v}
	}
	return varstore.Layer{Scope: varstore.ScopeData, Name: "data", Vars: vars}
}
