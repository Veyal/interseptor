package varstore

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/redact"
)

// Policy decides what happens to a {{name}} that cannot be resolved.
type Policy int

const (
	// PolicyBlock (the default) leaves the text in Value but sets Result.Err so
	// the caller refuses to send.
	PolicyBlock Policy = iota
	// PolicyLiteral sends the unresolved {{name}} text as-is (Postman behavior).
	PolicyLiteral
	// PolicyEmpty replaces unresolved references with an empty string.
	PolicyEmpty
)

// Default limits.
const (
	DefaultMaxDepth      = 8
	DefaultMaxOutput     = 1 << 20
	DefaultMaxExpansions = 10000
)

// Errors reported through Result.Err.
var (
	ErrUnresolved = errors.New("varstore: unresolved variables")
	ErrTooLarge   = errors.New("varstore: expansion exceeds output or work limit")
)

// UnresolvedError lists the names that blocked a send. It never carries values.
type UnresolvedError struct{ Names []string }

func (e *UnresolvedError) Error() string {
	return "unresolved variables: " + strings.Join(e.Names, ", ")
}
func (e *UnresolvedError) Unwrap() error { return ErrUnresolved }

// Options configures a Resolver. Zero values pick safe defaults.
type Options struct {
	Clock         func() time.Time // injectable for deterministic $timestamp
	Rand          *Rand            // injectable/seedable for dynamic variables
	Policy        Policy
	Registry      *redact.Registry // secret values used are added here
	MaxDepth      int
	MaxOutput     int
	MaxExpansions int
}

// Resolver expands templates. It holds no per-call state and is safe for
// concurrent use.
type Resolver struct{ o Options }

// New returns a Resolver with defaults filled in.
func New(o Options) *Resolver {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Rand == nil {
		o.Rand = NewRandomRand()
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = DefaultMaxOutput
	}
	if o.MaxExpansions <= 0 {
		o.MaxExpansions = DefaultMaxExpansions
	}
	return &Resolver{o: o}
}

// Use records one variable reference. It never holds the value.
type Use struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"` // scope name or "dynamic"
	Secret  bool   `json:"secret,omitempty"`
	Field   string `json:"field,omitempty"`
	Dynamic bool   `json:"dynamic,omitempty"`
}

// Result is the outcome of resolving one template.
type Result struct {
	Value      string   `json:"value"`
	Uses       []Use    `json:"uses,omitempty"`
	Unresolved []string `json:"unresolved,omitempty"`
	Problems   []string `json:"problems,omitempty"` // cycle, depth, unknown pipe
	Err        error    `json:"-"`
}

// Resolve expands template against stack.
func (r *Resolver) Resolve(template string, stack *Stack) Result {
	x := r.newRun(stack)
	x.field = ""
	var res Result
	res.Value = x.expand(template, 0, nil)
	return x.finish(res)
}

type run struct {
	r         *Resolver
	st        *Stack
	field     string
	uses      []Use
	unres     []string
	problems  []string
	expands   int
	produced  int
	fatal     error
	unresSeen map[string]struct{}
}

func (r *Resolver) newRun(st *Stack) *run {
	return &run{r: r, st: st, unresSeen: map[string]struct{}{}}
}

func (x *run) finish(res Result) Result {
	res.Uses, res.Unresolved, res.Problems = x.uses, x.unres, x.problems
	switch {
	case x.fatal != nil:
		res.Value, res.Err = "", x.fatal
	case x.r.o.Policy == PolicyBlock && len(x.unres) > 0:
		res.Err = &UnresolvedError{Names: append([]string(nil), x.unres...)}
	}
	return res
}

func (x *run) markUnresolved(name string) {
	if _, ok := x.unresSeen[name]; ok {
		return
	}
	x.unresSeen[name] = struct{}{}
	x.unres = append(x.unres, name)
}

func (x *run) problem(format string, a ...any) {
	x.problems = append(x.problems, fmt.Sprintf(format, a...))
}

func (x *run) write(sb *strings.Builder, s string) bool {
	x.produced += len(s)
	if x.produced > x.r.o.MaxOutput*DefaultMaxDepth || sb.Len()+len(s) > x.r.o.MaxOutput {
		x.fatal = fmt.Errorf("%w (limit %d bytes)", ErrTooLarge, x.r.o.MaxOutput)
		return false
	}
	sb.WriteString(s)
	return true
}

// matchEnd returns the index of the "}}" closing an expression that starts at
// i, honouring nested "{{ }}", or -1.
func matchEnd(s string, i int) int {
	depth := 0
	for i < len(s)-1 {
		switch {
		case s[i] == '{' && s[i+1] == '{':
			depth++
			i += 2
		case s[i] == '}' && s[i+1] == '}':
			if depth == 0 {
				return i
			}
			depth--
			i += 2
		default:
			i++
		}
	}
	return -1
}

func (x *run) expand(tpl string, depth int, chain []string) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	var sb strings.Builder
	for i := 0; i < len(tpl) && x.fatal == nil; {
		if strings.HasPrefix(tpl[i:], `\{{`) {
			if !x.write(&sb, "{{") {
				break
			}
			i += 3
			continue
		}
		if strings.HasPrefix(tpl[i:], "{{") {
			if j := matchEnd(tpl, i+2); j >= 0 {
				expr := tpl[i+2 : j]
				out, ok := x.eval(expr, depth, chain)
				if !ok {
					out = x.unresolvedText(expr)
				}
				if !x.write(&sb, out) {
					break
				}
				i = j + 2
				continue
			}
		}
		if !x.write(&sb, tpl[i:i+1]) {
			break
		}
		i++
	}
	return sb.String()
}

func (x *run) unresolvedText(expr string) string {
	if x.r.o.Policy == PolicyEmpty {
		return ""
	}
	return "{{" + expr + "}}"
}

func (x *run) eval(expr string, depth int, chain []string) (string, bool) {
	x.expands++
	if x.expands > x.r.o.MaxExpansions {
		x.fatal = fmt.Errorf("%w (more than %d expansions)", ErrTooLarge, x.r.o.MaxExpansions)
		return "", false
	}
	if strings.Contains(expr, "{{") { // nested name: resolve the inner first
		if depth+1 > x.r.o.MaxDepth {
			x.problem("depth limit %d exceeded", x.r.o.MaxDepth)
			x.markUnresolved(strings.TrimSpace(expr))
			return "", false
		}
		before := len(x.unres)
		inner := x.expand(expr, depth+1, chain)
		if len(x.unres) > before {
			return "", false
		}
		expr = inner
	}
	parts := strings.Split(expr, "|")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", false
	}
	val, secret, use, ok := x.lookup(name, depth, chain)
	if !ok {
		return "", false
	}
	for _, p := range parts[1:] {
		out, known := applyPipe(val, strings.TrimSpace(p))
		if !known {
			x.problem("unknown pipe %q on %q", strings.TrimSpace(p), name)
			x.markUnresolved(name + "|" + strings.TrimSpace(p))
			return "", false
		}
		val = out
	}
	use.Field = x.field
	x.uses = append(x.uses, use)
	if secret {
		x.r.o.Registry.Add(val)
	}
	return val, true
}

func (x *run) lookup(name string, depth int, chain []string) (val string, secret bool, use Use, ok bool) {
	if strings.HasPrefix(name, "$") {
		v, found := dynamic(name, x.r.o.Clock(), x.r.o.Rand)
		if !found {
			x.markUnresolved(name)
			return "", false, Use{}, false
		}
		return v, false, Use{Name: name, Scope: "dynamic", Dynamic: true}, true
	}
	v, scope, found := x.st.Lookup(name)
	if !found {
		x.markUnresolved(name)
		return "", false, Use{}, false
	}
	for _, c := range chain {
		if c == name {
			x.problem("cycle: %s -> %s", strings.Join(chain, " -> "), name)
			x.markUnresolved(name)
			return "", false, Use{}, false
		}
	}
	if depth+1 > x.r.o.MaxDepth {
		x.problem("depth limit %d exceeded at %q", x.r.o.MaxDepth, name)
		x.markUnresolved(name)
		return "", false, Use{}, false
	}
	next := append(append([]string(nil), chain...), name)
	val = x.expand(v.Value, depth+1, next)
	if v.Secret {
		x.r.o.Registry.Add(v.Value)
	}
	return val, v.Secret, Use{Name: name, Scope: scope.String(), Secret: v.Secret}, true
}
