// Package activescript runs user-authored ACTIVE scanner checks written in
// Starlark — the active twin of internal/checkscript. A custom active check
// defines:
//
//	def check(point, baseline, probe):
//	    r = probe("'")              # send a payload at this injection point
//	    if re_search("(?i)SQL syntax", r.body):
//	        return [finding("High", "SQL injection (custom)", evidence=r.body[:80])]
//	    return []
//
// `probe(payload)` sends one mutated request through the engine's sender (so it's
// recorded, session-auth applied, and counts against the run's request budget).
// Starlark is sandboxed: no files, sockets, clock, or imports — only the builtins
// we hand it (finding, re_search). Execution is step-bounded to stop runaway loops.
package activescript

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.starlark.net/starlark"

	"github.com/Veyal/interseptor/internal/activescan"
	"github.com/Veyal/interseptor/internal/starx"
)

// maxSteps bounds a single check run so a pathological script can't hang a scan.
const maxSteps = 5_000_000

// Check is a compiled user-authored active check. Run matches activescan.Check.Run
// so a Check can be used directly as an activescan.Check (wrapped with its ID).
type Check struct {
	ID string
	fn starlark.Value
}

func predeclared() starlark.StringDict {
	return starx.Predeclared()
}

// Compile parses and compiles an active check, requiring a callable
// check(point, baseline, probe). Module top-level code can't use for/while
// loops, but comprehensions are legal there, so the compile thread is
// step-bounded the same as Run's — otherwise a runaway comprehension at
// module scope could hang or OOM the process before check() is ever called.
func Compile(id, src string) (*Check, error) {
	thread := &starlark.Thread{Name: "compile:active:" + id} // no Load ⇒ load() disabled
	thread.SetMaxExecutionSteps(maxSteps)
	globals, err := starlark.ExecFile(thread, id+".star", src, predeclared())
	if err != nil {
		return nil, starx.ScriptError(fmt.Sprintf("active check %q", id), err)
	}
	fn, ok := globals["check"]
	if !ok {
		return nil, fmt.Errorf("active check %q: missing a `check(point, baseline, probe)` function", id)
	}
	if _, ok := fn.(starlark.Callable); !ok {
		return nil, fmt.Errorf("active check %q: `check` must be a function", id)
	}
	globals.Freeze()
	return &Check{ID: id, fn: fn}, nil
}

// Run executes the check against one injection point. probe sends payloads through
// the engine (budget/cancellation honoured by the caller's Prober). A check that
// returns multiple findings reports the highest-severity one as the Hit (carrying
// its own title/severity/detail/fix so each finding is self-describing).
func (c *Check) Run(p activescan.Point, baseline activescan.Response, probe activescan.Prober) (hit *activescan.Hit) {
	defer func() {
		if r := recover(); r != nil {
			hit = nil
		}
	}()
	thread := &starlark.Thread{Name: "run:active:" + c.ID}
	thread.SetMaxExecutionSteps(maxSteps)

	var lastFlow int64 // FlowID of the most recent probe — used to attribute findings
	probeBuiltin := starlark.NewBuiltin("probe", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var payload string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "payload", &payload); err != nil {
			return nil, err
		}
		resp := probe(payload)
		if resp.FlowID != 0 {
			lastFlow = resp.FlowID
		}
		return &responseValue{r: resp}, nil
	})
	res, err := starlark.Call(thread, c.fn, starlark.Tuple{
		&pointValue{p: p}, &responseValue{r: baseline}, probeBuiltin,
	}, nil)
	if err != nil {
		return nil
	}
	fs := collectFindings(res)
	if len(fs) == 0 {
		return nil
	}
	best := pickHighest(fs)
	if best.FlowID == 0 {
		best.FlowID = lastFlow // attribute to the confirming probe
	}
	return &activescan.Hit{
		Evidence: best.Evidence, FlowID: best.FlowID,
		Title: best.Title, Severity: best.Severity, Detail: best.Detail, Fix: best.Fix,
	}
}

// finding is what a script's finding(...) produces, in activescan terms.
type finding struct {
	Severity, Title, Detail, Evidence, Fix string
	FlowID                                 int64
}

func collectFindings(res starlark.Value) []finding {
	if res == nil || res == starlark.None {
		return nil
	}
	iter, ok := res.(starlark.Iterable)
	if !ok {
		return nil
	}
	it := iter.Iterate()
	defer it.Done()
	var out []finding
	var x starlark.Value
	for it.Next(&x) {
		d, ok := x.(*starlark.Dict)
		if !ok {
			continue
		}
		out = append(out, findingFromDict(d))
	}
	return out
}

func findingFromDict(d *starlark.Dict) finding {
	get := func(k string) string {
		if v, ok, _ := d.Get(starlark.String(k)); ok {
			if s, ok := starlark.AsString(v); ok {
				return s
			}
		}
		return ""
	}
	return finding{
		Severity: normSeverity(get("severity")),
		Title:    get("title"),
		Detail:   get("detail"),
		Evidence: get("evidence"),
		Fix:      get("fix"),
	}
}

func pickHighest(fs []finding) finding {
	rank := map[string]int{"High": 0, "Medium": 1, "Low": 2, "Info": 3}
	best := fs[0]
	for _, f := range fs[1:] {
		if rank[f.Severity] < rank[best.Severity] {
			best = f
		}
	}
	return best
}

func normSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high", "critical":
		return "High"
	case "medium", "med":
		return "Medium"
	case "low":
		return "Low"
	default:
		return "Info"
	}
}

// ---- values exposed to scripts ----

type pointValue struct{ p activescan.Point }

func (v *pointValue) String() string       { return fmt.Sprintf("point(%s %s)", v.p.Kind, v.p.Name) }
func (v *pointValue) Type() string         { return "point" }
func (v *pointValue) Freeze()              {}
func (v *pointValue) Truth() starlark.Bool { return starlark.True }
func (v *pointValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("point is unhashable")
}
func (v *pointValue) AttrNames() []string { return []string{"kind", "name", "value"} }
func (v *pointValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "kind":
		return starlark.String(v.p.Kind), nil
	case "name":
		return starlark.String(v.p.Name), nil
	case "value":
		return starlark.String(v.p.Value), nil
	}
	return nil, nil
}

// responseValue wraps an activescan.Response for both `baseline` and probe results.
type responseValue struct{ r activescan.Response }

func (v *responseValue) String() string       { return fmt.Sprintf("response(%d)", v.r.Status) }
func (v *responseValue) Type() string         { return "response" }
func (v *responseValue) Freeze()              {}
func (v *responseValue) Truth() starlark.Bool { return v.r.Status != 0 }
func (v *responseValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("response is unhashable")
}
func (v *responseValue) AttrNames() []string {
	return []string{"status", "body", "headers", "duration_ms", "flow_id", "header", "header_all"}
}
func (v *responseValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "status":
		return starlark.MakeInt(v.r.Status), nil
	case "body":
		return starlark.String(v.r.Body), nil
	case "headers":
		return headersDict(v.r.Headers), nil
	case "duration_ms":
		return starlark.MakeInt(int(v.r.Duration / time.Millisecond)), nil
	case "flow_id":
		return starlark.MakeInt(int(v.r.FlowID)), nil
	case "header":
		return headerGetter("header", v.r.Headers), nil
	case "header_all":
		return headerAllGetter("header_all", v.r.Headers), nil
	}
	return nil, nil
}

func headersDict(h http.Header) starlark.Value {
	d := starlark.NewDict(len(h))
	for k, vals := range h {
		first := ""
		if len(vals) > 0 {
			first = vals[0]
		}
		d.SetKey(starlark.String(http.CanonicalHeaderKey(k)), starlark.String(first))
	}
	d.Freeze()
	return d
}

func headerGetter(name string, h http.Header) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var key string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &key); err != nil {
			return nil, err
		}
		for k, vals := range h {
			if strings.EqualFold(k, key) && len(vals) > 0 {
				return starlark.String(vals[0]), nil
			}
		}
		return starlark.String(""), nil
	})
}

func headerAllGetter(name string, h http.Header) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var key string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &key); err != nil {
			return nil, err
		}
		for k, vals := range h {
			if strings.EqualFold(k, key) {
				out := make([]starlark.Value, len(vals))
				for i, v := range vals {
					out[i] = starlark.String(v)
				}
				return starlark.NewList(out), nil
			}
		}
		return starlark.NewList(nil), nil
	})
}

var (
	_ starlark.HasAttrs = (*pointValue)(nil)
	_ starlark.HasAttrs = (*responseValue)(nil)
)
