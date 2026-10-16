package varstore

import (
	"sort"

	"github.com/Veyal/interseptor/internal/redact"
)

// Scope orders variable layers. A lower value wins.
type Scope int

const (
	ScopeLocal Scope = iota
	ScopeData
	ScopeEnvironment
	ScopeFolder
	ScopeCollection
	ScopeGlobal
)

var scopeNames = [...]string{"local", "data", "environment", "folder", "collection", "global"}

func (s Scope) String() string {
	if s < 0 || int(s) >= len(scopeNames) {
		return "unknown"
	}
	return scopeNames[s]
}

// Var is one effective (current-else-initial) variable value.
type Var struct {
	Value  string
	Secret bool
}

// Layer is the variables of one scope owner (an environment, a folder...).
type Layer struct {
	Scope Scope
	Name  string
	Vars  map[string]Var
}

// Stack is an ordered set of layers. It is immutable once built by the
// resolver's callers; build a new one per request.
type Stack struct {
	layers []Layer
}

// NewStack builds a stack from layers. Layers sort by scope; layers of the
// same scope keep the order given, so add folders inner to outer.
func NewStack(layers ...Layer) *Stack {
	s := &Stack{layers: append([]Layer(nil), layers...)}
	sort.SliceStable(s.layers, func(i, j int) bool { return s.layers[i].Scope < s.layers[j].Scope })
	return s
}

// Lookup returns the winning variable for name and the scope it came from.
func (s *Stack) Lookup(name string) (Var, Scope, bool) {
	if s == nil {
		return Var{}, 0, false
	}
	for _, l := range s.layers {
		if v, ok := l.Vars[name]; ok {
			return v, l.Scope, true
		}
	}
	return Var{}, 0, false
}

// RegisterSecrets adds every secret variable value to reg so later masking
// covers values even when they never pass through a template.
func (s *Stack) RegisterSecrets(reg *redact.Registry) {
	if s == nil || reg == nil {
		return
	}
	for _, l := range s.layers {
		for _, v := range l.Vars {
			if v.Secret {
				reg.Add(v.Value)
			}
		}
	}
}

// Names lists every defined variable name, sorted, for autocompletion.
func (s *Stack) Names() []string {
	if s == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, l := range s.layers {
		for k := range l.Vars {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
