// Package searchscript runs sandboxed Starlark predicates over captured flows.
package searchscript

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.starlark.net/starlark"

	"github.com/Veyal/interseptor/internal/starx"
)

const (
	maxSourceBytes = 64 << 10
	maxSteps       = 100_000
)

// Flow is immutable input exposed to a search predicate.
type Flow struct {
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

// Script is a compiled match(flow) predicate.
type Script struct{ fn starlark.Callable }

// Compile compiles src once and requires a callable match(flow) predicate.
func Compile(src string) (script *Script, err error) {
	if len(src) > maxSourceBytes {
		return nil, fmt.Errorf("search script: source exceeds %d KiB", maxSourceBytes>>10)
	}
	defer func() {
		if recover() != nil {
			script = nil
			err = fmt.Errorf("search script: compilation failed")
		}
	}()

	thread := newThread("compile:search")
	globals, err := starlark.ExecFile(thread, "search.star", src, nil)
	if err != nil {
		return nil, scriptError(err)
	}
	value, ok := globals["match"]
	if !ok {
		return nil, fmt.Errorf("search script: missing a `match(flow)` function")
	}
	fn, ok := value.(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("search script: `match` must be a function")
	}
	globals.Freeze()

	if err := validateResult(fn); err != nil {
		return nil, err
	}
	return &Script{fn: fn}, nil
}

func validateResult(fn starlark.Callable) error {
	result, err := starlark.Call(newThread("validate:search"), fn, starlark.Tuple{newFlowValue(Flow{})}, nil)
	if err != nil {
		return scriptError(err)
	}
	if _, ok := result.(starlark.Bool); !ok {
		return fmt.Errorf("search script: `match(flow)` must return bool, got %s", result.Type())
	}
	return nil
}

// Match evaluates compiled predicate against flow. It never exposes flow content in errors.
func (s *Script) Match(flow Flow) (bool, error) {
	return s.MatchContext(context.Background(), flow)
}

// MatchContext evaluates a predicate and interrupts Starlark work when ctx ends.
func (s *Script) MatchContext(ctx context.Context, flow Flow) (matched bool, err error) {
	defer func() {
		if recover() != nil {
			matched = false
			err = fmt.Errorf("search script: execution failed")
		}
	}()
	if s == nil || s.fn == nil {
		return false, fmt.Errorf("search script: nil script")
	}
	thread := newThread("run:search")
	stop := context.AfterFunc(ctx, func() { thread.Cancel("request cancelled") })
	defer stop()
	result, err := starlark.Call(thread, s.fn, starlark.Tuple{newFlowValue(flow)}, nil)
	if err != nil {
		return false, scriptError(err)
	}
	resultBool, ok := result.(starlark.Bool)
	if !ok {
		return false, fmt.Errorf("search script: `match(flow)` must return bool, got %s", result.Type())
	}
	return bool(resultBool), nil
}

func newThread(name string) *starlark.Thread {
	thread := &starlark.Thread{Name: name} // No Load callback: load() is unavailable.
	thread.SetMaxExecutionSteps(maxSteps)
	return thread
}

func scriptError(err error) error {
	return starx.ScriptError("search script", err)
}

type flowValue struct{ flow Flow }

func newFlowValue(flow Flow) *flowValue {
	return &flowValue{flow: Flow{
		Method: flow.Method, Scheme: flow.Scheme, Host: flow.Host, Port: flow.Port, Path: flow.Path, Status: flow.Status, Mime: flow.Mime,
		ReqHeaders: copyHeaders(flow.ReqHeaders), ResHeaders: copyHeaders(flow.ResHeaders), ReqBody: flow.ReqBody, ResBody: flow.ResBody,
	}}
}

func copyHeaders(headers map[string][]string) map[string][]string {
	copied := make(map[string][]string, len(headers))
	for key, values := range headers {
		copied[key] = append([]string(nil), values...)
	}
	return copied
}

func (v *flowValue) String() string      { return "flow" }
func (*flowValue) Type() string          { return "flow" }
func (*flowValue) Freeze()               {}
func (*flowValue) Truth() starlark.Bool  { return starlark.True }
func (*flowValue) Hash() (uint32, error) { return 0, fmt.Errorf("flow is unhashable") }
func (*flowValue) AttrNames() []string {
	return []string{"method", "scheme", "host", "port", "path", "status", "mime", "req_body", "res_body", "req_headers", "res_headers", "req_header", "res_header", "req_header_all", "res_header_all", "query_param"}
}

func (v *flowValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "method":
		return starlark.String(v.flow.Method), nil
	case "scheme":
		return starlark.String(v.flow.Scheme), nil
	case "host":
		return starlark.String(v.flow.Host), nil
	case "port":
		return starlark.MakeInt(v.flow.Port), nil
	case "path":
		return starlark.String(v.flow.Path), nil
	case "status":
		return starlark.MakeInt(v.flow.Status), nil
	case "mime":
		return starlark.String(v.flow.Mime), nil
	case "req_body":
		return starlark.String(v.flow.ReqBody), nil
	case "res_body":
		return starlark.String(v.flow.ResBody), nil
	case "req_headers":
		return headersDict(v.flow.ReqHeaders), nil
	case "res_headers":
		return headersDict(v.flow.ResHeaders), nil
	case "req_header":
		return headerGetter("req_header", v.flow.ReqHeaders), nil
	case "res_header":
		return headerGetter("res_header", v.flow.ResHeaders), nil
	case "req_header_all":
		return headerAllGetter("req_header_all", v.flow.ReqHeaders), nil
	case "res_header_all":
		return headerAllGetter("res_header_all", v.flow.ResHeaders), nil
	case "query_param":
		return queryParamGetter(v.flow.Path), nil
	default:
		return nil, nil
	}
}

func headersDict(headers map[string][]string) starlark.Value {
	dict := starlark.NewDict(len(headers))
	for key, values := range headers {
		first := ""
		if len(values) > 0 {
			first = values[0]
		}
		if err := dict.SetKey(starlark.String(http.CanonicalHeaderKey(key)), starlark.String(first)); err != nil {
			panic(err)
		}
	}
	dict.Freeze()
	return dict
}

func headerGetter(name string, headers map[string][]string) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(_ *starlark.Thread, builtin *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(builtin.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		for key, values := range headers {
			if strings.EqualFold(key, name) && len(values) > 0 {
				return starlark.String(values[0]), nil
			}
		}
		return starlark.String(""), nil
	})
}

func headerAllGetter(name string, headers map[string][]string) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(_ *starlark.Thread, builtin *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(builtin.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		for key, values := range headers {
			if strings.EqualFold(key, name) {
				out := make([]starlark.Value, len(values))
				for index, value := range values {
					out[index] = starlark.String(value)
				}
				list := starlark.NewList(out)
				list.Freeze()
				return list, nil
			}
		}
		return starlark.Tuple{}, nil
	})
}

func queryParamGetter(path string) *starlark.Builtin {
	return starlark.NewBuiltin("query_param", func(_ *starlark.Thread, builtin *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(builtin.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		queryStart := strings.IndexByte(path, '?')
		if queryStart < 0 {
			return starlark.String(""), nil
		}
		values, err := url.ParseQuery(path[queryStart+1:])
		if err != nil {
			return starlark.String(""), nil
		}
		return starlark.String(values.Get(name)), nil
	})
}

var _ starlark.HasAttrs = (*flowValue)(nil)
