// Package checkscript runs user-authored passive scanner checks written in
// Starlark — a small, Python-like, deterministic language. It is the extension
// "standard": every custom check is a Starlark file that defines
//
//	def check(flow):
//	    # inspect flow, return a list of finding(...) (or [])
//
// Starlark is sandboxed by design — a check cannot read files, open sockets, see
// the clock, or import anything we don't hand it — so checks are safe to share
// and run. Execution is additionally bounded (step limit) to stop runaway loops.
package checkscript

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.starlark.net/starlark"

	"github.com/Veyal/interseptor/internal/starx"
	"github.com/Veyal/interseptor/internal/store"
)

// maxSteps bounds a single check run so a pathological script can't hang a scan.
const maxSteps = 5_000_000

// Flow is the read-only view of a captured exchange handed to a check.
type Flow struct {
	ID         int64
	Method     string
	Scheme     string
	Host       string
	Port       int
	Path       string
	Status     int
	Mime       string
	ReqHeaders map[string][]string
	ResHeaders map[string][]string
	ReqBody    string
	ResBody    string
}

// Check is a compiled Starlark check.
type Check struct {
	ID string
	fn starlark.Value
}

// predeclared returns the builtins every check can use. The shared standard
// library (finding, re_search, json_*, b64*, url_*, hash, hmac) lives in
// internal/starx so every passive check exposes the same standard surface.
func predeclared() starlark.StringDict {
	return starx.Predeclared()
}

// Compile parses and compiles a check's source, validating that it defines a
// callable check(flow). The script's top level runs once here (sandboxed).
// Module top-level code can't use for/while loops, but comprehensions are
// legal there, so the compile thread is step-bounded the same as Run's —
// otherwise a runaway comprehension at module scope could hang or OOM the
// process before a single check(flow) is ever called.
func Compile(id, src string) (*Check, error) {
	c, logs, err := CompileWithLogs(id, src)
	for _, line := range logs {
		log.Printf("check %s (compile): %s", id, line)
	}
	return c, err
}

// CompileWithLogs is Compile, also returning whatever the module's top level
// printed. A `print()` at module scope is the usual "did this file even load"
// debug, so the check editor shows it next to the compile result.
func CompileWithLogs(id, src string) (*Check, []string, error) {
	out := &printLog{}
	thread := &starlark.Thread{Name: "compile:" + id, Print: out.add} // no Load func ⇒ load() is disabled
	thread.SetMaxExecutionSteps(maxSteps)
	globals, err := starlark.ExecFile(thread, id+".star", src, predeclared())
	if err != nil {
		return nil, out.out(), starx.ScriptError(fmt.Sprintf("check %q", id), err)
	}
	fn, ok := globals["check"]
	if !ok {
		return nil, out.out(), fmt.Errorf("check %q: missing a `check(flow)` function", id)
	}
	if _, ok := fn.(starlark.Callable); !ok {
		return nil, out.out(), fmt.Errorf("check %q: `check` must be a function", id)
	}
	globals.Freeze()
	return &Check{ID: id, fn: fn}, out.out(), nil
}

// LoadDir compiles every `*.star` check in dir (sorted by name for determinism).
// It returns the compiled checks plus a filename→error map for any that failed
// to compile, so the caller can surface broken checks without aborting the rest.
// A missing directory yields no checks and no error.
func LoadDir(dir string) ([]*Check, map[string]error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var checks []*Check
	var errs map[string]error
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			var c *Check
			c, err = Compile(strings.TrimSuffix(name, ".star"), string(src))
			if err == nil {
				checks = append(checks, c)
				continue
			}
		}
		if errs == nil {
			errs = map[string]error{}
		}
		errs[name] = err
	}
	return checks, errs
}

// Run executes the check against one flow and returns its findings. A runtime
// error (bad type, exceeded step limit, …) is returned, not panicked. Anything
// the check printed goes to the server log, attributed to the check and flow,
// instead of raw stderr — use RunWithLogs to get it back as data.
func (c *Check) Run(f Flow) ([]store.Issue, error) {
	issues, logs, err := c.RunWithLogs(f)
	for _, line := range logs {
		log.Printf("check %s on flow %d: %s", c.ID, f.ID, line)
	}
	return issues, err
}

// RunWithLogs executes the check and returns its findings together with its
// bounded print() output. The output comes back even when the check errors —
// what it printed before failing is usually the reason it failed.
func (c *Check) RunWithLogs(f Flow) (issues []store.Issue, logs []string, err error) {
	printed := &printLog{}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("check %q panicked: %v", c.ID, r)
		}
		logs = printed.out()
	}()
	thread := &starlark.Thread{Name: "run:" + c.ID, Print: printed.add}
	thread.SetMaxExecutionSteps(maxSteps)
	res, err := starlark.Call(thread, c.fn, starlark.Tuple{&flowValue{f}}, nil)
	if err != nil {
		return nil, nil, starx.ScriptError(fmt.Sprintf("check %q", c.ID), err)
	}
	out, err := collect(res)
	if err != nil {
		return nil, nil, starx.ScriptError(fmt.Sprintf("check %q", c.ID), err)
	}
	target := f.Method + " " + f.Host + f.Path
	for i := range out {
		out[i].FlowID = f.ID
		if out[i].Target == "" {
			out[i].Target = target
		}
	}
	return out, nil, nil
}

func collect(res starlark.Value) ([]store.Issue, error) {
	if res == nil || res == starlark.None {
		return nil, nil
	}
	iter, ok := res.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("check must return a list of finding(...), got %s", res.Type())
	}
	it := iter.Iterate()
	defer it.Done()
	var out []store.Issue
	var x starlark.Value
	for it.Next(&x) {
		d, ok := x.(*starlark.Dict)
		if !ok {
			return nil, fmt.Errorf("each finding must be a finding(...), got %s", x.Type())
		}
		out = append(out, issueFromDict(d))
	}
	return out, nil
}

func issueFromDict(d *starlark.Dict) store.Issue {
	get := func(k string) string {
		if v, ok, _ := d.Get(starlark.String(k)); ok {
			if s, ok := starlark.AsString(v); ok {
				return s
			}
		}
		return ""
	}
	return store.Issue{
		Severity: normSeverity(get("severity")),
		Title:    get("title"),
		Detail:   get("detail"),
		Evidence: get("evidence"),
		Fix:      get("fix"),
	}
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

// ---- flow value exposed to scripts ----

type flowValue struct{ f Flow }

func (v *flowValue) String() string {
	return fmt.Sprintf("flow(%s %s%s)", v.f.Method, v.f.Host, v.f.Path)
}
func (v *flowValue) Type() string          { return "flow" }
func (v *flowValue) Freeze()               {}
func (v *flowValue) Truth() starlark.Bool  { return starlark.True }
func (v *flowValue) Hash() (uint32, error) { return 0, fmt.Errorf("flow is unhashable") }

func (v *flowValue) AttrNames() []string {
	return []string{
		"method", "scheme", "host", "port", "path", "status", "mime",
		"req_body", "res_body", "req_headers", "res_headers",
		"req_header", "res_header", "req_header_all", "res_header_all",
		"query_param", "query_params", "cookies",
	}
}

func (v *flowValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "method":
		return starlark.String(v.f.Method), nil
	case "scheme":
		return starlark.String(v.f.Scheme), nil
	case "host":
		return starlark.String(v.f.Host), nil
	case "port":
		return starlark.MakeInt(v.f.Port), nil
	case "path":
		return starlark.String(v.f.Path), nil
	case "status":
		return starlark.MakeInt(v.f.Status), nil
	case "mime":
		return starlark.String(v.f.Mime), nil
	case "req_body":
		return starlark.String(v.f.ReqBody), nil
	case "res_body":
		return starlark.String(v.f.ResBody), nil
	case "req_headers":
		return headersDict(v.f.ReqHeaders), nil
	case "res_headers":
		return headersDict(v.f.ResHeaders), nil
	case "req_header":
		return headerGetter("req_header", v.f.ReqHeaders), nil
	case "res_header":
		return headerGetter("res_header", v.f.ResHeaders), nil
	case "req_header_all":
		return headerAllGetter("req_header_all", v.f.ReqHeaders), nil
	case "res_header_all":
		return headerAllGetter("res_header_all", v.f.ResHeaders), nil
	case "query_param":
		return queryParamGetter(v.f.Path), nil
	case "query_params":
		return queryParamsDict(v.f.Path), nil
	case "cookies":
		return cookiesDict(v.f.ReqHeaders), nil
	}
	return nil, nil // unknown attribute ⇒ Starlark raises AttributeError
}

func headersDict(h map[string][]string) starlark.Value {
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

func headerGetter(name string, h map[string][]string) *starlark.Builtin {
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

func headerAllGetter(name string, h map[string][]string) *starlark.Builtin {
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

func queryParamGetter(path string) *starlark.Builtin {
	return starlark.NewBuiltin("query_param", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		if i := strings.IndexByte(path, '?'); i >= 0 {
			if vals, err := url.ParseQuery(path[i+1:]); err == nil {
				return starlark.String(vals.Get(name)), nil
			}
		}
		return starlark.String(""), nil
	})
}

// queryParamsDict exposes the whole query string as name → list of values.
// It is a list per name, not a single string, because a query string can
// repeat a name meaningfully (?id=1&id=2) and a check that enumerates
// parameters — looking for a reflected value, a redirect target — has to see
// both. Values are url-decoded; flow.query_param(name) remains the
// first-value convenience. Insertion order follows the query string, and
// Starlark dicts keep it, so iteration is deterministic.
func queryParamsDict(path string) starlark.Value {
	d := starlark.NewDict(0)
	i := strings.IndexByte(path, '?')
	if i < 0 {
		d.Freeze()
		return d
	}
	for _, kv := range strings.Split(path[i+1:], "&") {
		if kv == "" {
			continue
		}
		raw, rawVal, _ := strings.Cut(kv, "=")
		name, err := url.QueryUnescape(raw)
		if err != nil {
			name = raw
		}
		val, err := url.QueryUnescape(rawVal)
		if err != nil {
			val = rawVal
		}
		key := starlark.String(name)
		existing, found, err := d.Get(key)
		if err != nil {
			continue
		}
		vals := []starlark.Value{}
		if found {
			if list, ok := existing.(*starlark.List); ok {
				for n := 0; n < list.Len(); n++ {
					vals = append(vals, list.Index(n))
				}
			}
		}
		vals = append(vals, starlark.String(val))
		_ = d.SetKey(key, starlark.NewList(vals))
	}
	d.Freeze()
	return d
}

// cookiesDict exposes the request's cookies as name → value. Unlike a query
// string, a Cookie header does not repeat names meaningfully, so this is a
// plain string per name — the first occurrence wins — matching req_headers.
// Malformed segments are skipped rather than failing the check.
func cookiesDict(h map[string][]string) starlark.Value {
	d := starlark.NewDict(0)
	for key, vals := range h {
		if !strings.EqualFold(key, "Cookie") {
			continue
		}
		for _, header := range vals {
			for _, part := range strings.Split(header, ";") {
				name, val, ok := strings.Cut(strings.TrimSpace(part), "=")
				if !ok || name == "" {
					continue
				}
				if _, found, err := d.Get(starlark.String(name)); err != nil || found {
					continue
				}
				_ = d.SetKey(starlark.String(name), starlark.String(strings.TrimSpace(val)))
			}
		}
	}
	d.Freeze()
	return d
}

var _ starlark.HasAttrs = (*flowValue)(nil)
