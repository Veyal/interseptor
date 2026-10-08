// Package pmsandbox runs Postman-compatible collection scripts (pm.*, legacy
// globals, Chai-subset assertions, CryptoJS/crypto/Buffer shims) inside the
// default-deny goja runtime from internal/jsrt.
//
// A script sees only what Input hands it. Network egress is the injected
// Sender (proxied sender behind the scope guard); there is no filesystem,
// process, environment, fetch or raw socket. Every phase gets a fresh runtime
// with the prelude compiled in; limits cover wall clock, call stack, source,
// console, value size, allocation helpers, sends per script and (best effort,
// in-process) heap growth.
//
// APIs that Postman has but this sandbox does not ship fail with status
// "unsupported" naming the API instead of a false pass or a false failure.
package pmsandbox

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

//go:embed prelude/*.js
var preludeFS embed.FS

var (
	preludeOnce sync.Once
	preludeSrc  string
)

// prelude returns the concatenated prelude wrapped in one closure.
func prelude() string {
	preludeOnce.Do(func() {
		ents, _ := preludeFS.ReadDir("prelude")
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		var sb strings.Builder
		sb.WriteString("(function(){'use strict';\n")
		for _, n := range names {
			b, _ := preludeFS.ReadFile("prelude/" + n)
			sb.Write(b)
			sb.WriteByte('\n')
		}
		sb.WriteString("})();")
		preludeSrc = sb.String()
	})
	return preludeSrc
}

// Limits bound one script run. Zero fields take defaults.
type Limits struct {
	// Timeout is the wall-clock interrupt. Default 5s, or 30s when the
	// script may send requests; capped at jsrt.MaxTimeout (60s).
	Timeout     time.Duration
	MaxSource   int // script source bytes (default 512 KiB)
	ConsoleMax  int // captured console bytes (default 256 KiB)
	MaxSends    int // pm.sendRequest calls (default 25)
	MaxTests    int // recorded test results (default 5000)
	MaxString   int // longest string repeat/pad may build (default 10 MiB)
	MaxArray    int // largest Array(n)/join (default 10M)
	MaxAlloc    int // Buffer/ArrayBuffer/typed array bytes (default 16 MiB)
	MaxResponse int // response bytes exposed to scripts (default 10 MiB)
	MaxOutput   int // bytes of test/console text kept in Output (default 1 MiB)
	// HeapGrowth caps process heap growth during the run (default 256 MiB).
	// It is a process-wide measurement, so concurrent runs can trip each
	// other; the re-exec'd worker (WP9) replaces it with a hard RSS limit.
	HeapGrowth uint64
}

func (l Limits) withDefaults(c scriptctx.Caps) Limits {
	if l.Timeout <= 0 {
		l.Timeout = jsrt.DefaultTimeout
		if c.NetSend {
			l.Timeout = 30 * time.Second
		}
	}
	if l.Timeout > jsrt.MaxTimeout {
		l.Timeout = jsrt.MaxTimeout
	}
	def := func(p *int, v int) {
		if *p <= 0 {
			*p = v
		}
	}
	def(&l.MaxSource, jsrt.DefaultMaxSource)
	def(&l.ConsoleMax, jsrt.DefaultConsoleMax)
	def(&l.MaxSends, 25)
	def(&l.MaxTests, 5000)
	def(&l.MaxString, 10<<20)
	def(&l.MaxArray, 10_000_000)
	def(&l.MaxAlloc, 16<<20)
	def(&l.MaxResponse, 10<<20)
	def(&l.MaxOutput, 1<<20)
	if l.HeapGrowth == 0 {
		l.HeapGrowth = 256 << 20
	}
	return l
}

// Input is everything one script run can see.
type Input struct {
	Phase  scriptctx.Phase
	Name   string // script name for stack traces, e.g. "request:Login/test"
	Script string

	Request  *scriptctx.Request
	Response *scriptctx.Response
	Vars     scriptctx.Vars
	Cookies  []scriptctx.Cookie
	Info     scriptctx.Info

	Caps   scriptctx.Caps
	Limits Limits

	// Clock and Rand make runs reproducible (Date, performance.now,
	// Math.random, uuid, CryptoJS.lib.WordArray.random, AES salts).
	Clock func() time.Time
	Rand  func() float64

	// Sender performs pm.sendRequest. nil means sends fail with an error.
	Sender scriptctx.Sender
	// ScopeCheck returns an error when url is out of scope. It runs before
	// every pm.sendRequest and backs isp.assertScope.
	ScopeCheck func(url string) error
	// Scrub is the caller's secret scrubber (the project-wide redact
	// function). It is applied in addition to the built-in masking of
	// secret-named variable values.
	Scrub func(string) string
}

// Script-level statuses reported in Output.Status.
const (
	StatusPass        = scriptctx.StatusPass
	StatusFail        = scriptctx.StatusFail
	StatusError       = scriptctx.StatusError
	StatusUnsupported = scriptctx.StatusUnsupported
)

// ScriptError is a script-level failure (uncaught exception, limit hit).
type ScriptError struct {
	Kind    string `json:"kind"` // error|unsupported|timeout|canceled|memory|limit|syntax
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// Output is the outcome of one run. Human-facing text (Console, Tests,
// Errors) is scrubbed; Vars, Changes and Request carry raw values because the
// pipeline commits them, and must not be logged or exported unscrubbed.
type Output struct {
	Status      string
	Tests       []scriptctx.TestResult
	Console     []scriptctx.ConsoleLine
	Errors      []ScriptError
	Unsupported []string
	Vars        scriptctx.Vars // final environment/globals/collection/local
	Changes     []scriptctx.VarChange
	Request     *scriptctx.Request // pre-request phase: the mutated request
	Flow        scriptctx.FlowControl
	CookieOps   []scriptctx.CookieOp
	Sends       []scriptctx.SendRecord
	Elapsed     time.Duration

	redact func(string) string
}

// Redact scrubs s with the same masking used for Output's own text. Callers
// use it for anything derived from Output that leaves the process.
func (o Output) Redact(s string) string {
	if o.redact == nil {
		return s
	}
	return o.redact(s)
}

type limitsWire struct {
	MaxTests  int `json:"maxTests"`
	MaxString int `json:"maxString"`
	MaxArray  int `json:"maxArray"`
	MaxAlloc  int `json:"maxAlloc"`
}

type inWire struct {
	Phase    scriptctx.Phase    `json:"phase"`
	Request  *scriptctx.Request `json:"request,omitempty"`
	Response *responseWire      `json:"response,omitempty"`
	Vars     scriptctx.Vars     `json:"vars"`
	Cookies  []scriptctx.Cookie `json:"cookies"`
	Info     scriptctx.Info     `json:"info"`
	Caps     scriptctx.Caps     `json:"caps"`
	Limits   limitsWire         `json:"limits"`
}

type responseWire struct {
	Code         int                `json:"code"`
	Status       string             `json:"status"`
	Headers      []scriptctx.Header `json:"headers"`
	Body         string             `json:"body"`
	ResponseTime float64            `json:"responseTime"`
	Size         int                `json:"size"`
	Cookies      []scriptctx.Cookie `json:"cookies"`
}

type outWire struct {
	Tests []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Message    string `json:"message"`
		Expected   string `json:"expected"`
		Actual     string `json:"actual"`
		DurationMs int64  `json:"durationMs"`
	} `json:"tests"`
	Changes     []scriptctx.VarChange `json:"changes"`
	Errors      []ScriptError         `json:"errors"`
	Unsupported []string              `json:"unsupported"`
	Vars        struct {
		Environment map[string]any `json:"environment"`
		Globals     map[string]any `json:"globals"`
		Collection  map[string]any `json:"collection"`
		Local       map[string]any `json:"local"`
	} `json:"vars"`
	Request   *scriptctx.Request    `json:"request"`
	Flow      scriptctx.FlowControl `json:"flow"`
	CookieOps []scriptctx.CookieOp  `json:"cookieOps"`
}

const finName = "__isp_fin"

// Run executes in.Script and returns the outcome. It never panics and never
// returns a Go error: failures are reported in Output.
func Run(ctx context.Context, in Input) (out Output) {
	begin := time.Now()
	in.Limits = in.Limits.withDefaults(in.Caps)
	if in.Info.EventName == "" {
		in.Info.EventName = string(in.Phase)
	}
	defer func() {
		if p := recover(); p != nil {
			out.Errors = append(out.Errors, ScriptError{Kind: "error", Message: "sandbox panic"})
			out.Status = StatusError
		}
		out.Elapsed = time.Since(begin)
	}()

	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopWatch := watchHeap(rctx, in.Limits.HeapGrowth, func() { cancel(errMemory) })
	defer stopWatch()

	h := newHost(rctx, &in)
	rt := jsrt.New(jsrt.Options{
		Timeout: in.Limits.Timeout, Clock: in.Clock, Rand: in.Rand,
		MaxSource: in.Limits.MaxSource + 4096, ConsoleMax: 1,
	})
	defer rt.Close()

	var scriptErrs []ScriptError
	fail := func(e error) {
		scriptErrs = append(scriptErrs, classify(rctx, e))
	}

	if err := bootstrap(rt, h, &in); err != nil {
		fail(err)
		return finalize(h, &in, nil, scriptErrs)
	}
	if _, err := rt.Eval(rctx, "prelude.js", prelude()); err != nil {
		fail(err)
		return finalize(h, &in, nil, scriptErrs)
	}
	if len(in.Script) > in.Limits.MaxSource {
		fail(jsrt.ErrSourceSize)
		return finalize(h, &in, nil, scriptErrs)
	}
	name := in.Name
	if name == "" {
		name = string(in.Phase) + ".js"
	}
	wrapper := "(async function(){try{" + in.Script + "\n;await globalThis." + finName + ".settle();}catch(e){globalThis." + finName + ".fail(e)}})()"
	if _, err := rt.Eval(rctx, name, wrapper); err != nil {
		fail(err)
	}
	// Collect results even after a limit hit: a fresh context keeps the
	// finalizer from being killed by the cancellation that stopped the script.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer fcancel()
	res, err := rt.Eval(fctx, "finish.js", "globalThis."+finName+".finish()")
	var wire *outWire
	if err == nil {
		if s, ok := res.Value.(string); ok {
			var w outWire
			if json.Unmarshal([]byte(s), &w) == nil {
				wire = &w
			}
		}
	}
	if wire == nil && len(scriptErrs) == 0 {
		scriptErrs = append(scriptErrs, ScriptError{Kind: "error", Message: "sandbox could not collect results"})
	}
	return finalize(h, &in, wire, scriptErrs)
}

var errMemory = errors.New("pmsandbox: heap growth limit exceeded")

func bootstrap(rt jsrt.Runtime, h *host, in *Input) error {
	body := ""
	var rw *responseWire
	if in.Response != nil {
		b := in.Response.Body
		if len(b) > in.Limits.MaxResponse {
			b = b[:in.Limits.MaxResponse]
		}
		body = string(b)
		rw = &responseWire{
			Code: in.Response.Code, Status: in.Response.Status, Headers: in.Response.Headers, Body: body,
			ResponseTime: float64(in.Response.ResponseTime.Microseconds()) / 1000, Size: len(in.Response.Body), Cookies: in.Response.Cookies,
		}
	}
	if in.Cookies == nil {
		in.Cookies = []scriptctx.Cookie{}
	}
	w := inWire{
		Phase: in.Phase, Request: in.Request, Response: rw, Vars: heapVars(in.Vars, in.Caps), Cookies: in.Cookies, Info: in.Info, Caps: in.Caps,
		Limits: limitsWire{MaxTests: in.Limits.MaxTests, MaxString: in.Limits.MaxString, MaxArray: in.Limits.MaxArray, MaxAlloc: in.Limits.MaxAlloc},
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	if err := rt.Set("__h", h.funcs()); err != nil {
		return err
	}
	if err := rt.Set("__in", string(b)); err != nil {
		return err
	}
	return rt.Set("__fin", finName)
}

func classify(ctx context.Context, e error) ScriptError {
	if c := context.Cause(ctx); errors.Is(c, errMemory) {
		return ScriptError{Kind: "memory", Message: errMemory.Error()}
	}
	var se *jsrt.ScriptError
	switch {
	case errors.Is(e, jsrt.ErrTimeout):
		return ScriptError{Kind: "timeout", Message: "script timed out"}
	case errors.Is(e, jsrt.ErrCanceled):
		return ScriptError{Kind: "canceled", Message: "script canceled"}
	case errors.Is(e, jsrt.ErrSourceSize):
		return ScriptError{Kind: "limit", Message: "script source too large"}
	case errors.Is(e, jsrt.ErrTimers):
		return ScriptError{Kind: "limit", Message: "too many timers"}
	case errors.As(e, &se):
		k := "error"
		if strings.Contains(se.Message, "SyntaxError") {
			k = "syntax"
		}
		return ScriptError{Kind: k, Message: se.Message, Stack: se.Stack}
	}
	return ScriptError{Kind: "error", Message: e.Error()}
}

// heapVars is the variable data that may enter the JS heap. Secret values
// never do unless the script holds secrets.read, and nothing does without
// vars.read, so no prelude bug can expose what the heap never held. The
// secret name list stays so replaceIn and masking keep working.
func heapVars(v scriptctx.Vars, caps scriptctx.Caps) scriptctx.Vars {
	out := scriptctx.Vars{EnvName: v.EnvName, Secret: v.Secret}
	if !caps.VarsRead {
		return out
	}
	drop := map[string]bool{}
	if !caps.SecretsRead {
		for _, n := range v.Secret {
			drop[n] = true
		}
	}
	keep := func(m map[string]any) map[string]any {
		if m == nil {
			return nil
		}
		o := make(map[string]any, len(m))
		for k, x := range m {
			if !drop[k] {
				o[k] = x
			}
		}
		return o
	}
	out.Environment, out.Globals, out.Collection = keep(v.Environment), keep(v.Globals), keep(v.Collection)
	out.Local, out.IterationData = keep(v.Local), keep(v.IterationData)
	return out
}
