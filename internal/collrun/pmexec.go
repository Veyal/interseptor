package collrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/scriptctx"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// Capability names a collection can grant its scripts (default deny).
const (
	CapVarsRead     = "vars.read"
	CapVarsWrite    = "vars.write"
	CapCookiesRead  = "cookies.read"
	CapCookiesWrite = "cookies.write"
	CapNetSend      = "net.send"
	CapSecretsRead  = "secrets.read"
)

func sandboxCaps(names []string) scriptctx.Caps {
	var c scriptctx.Caps
	for _, n := range names {
		switch n {
		case CapVarsRead:
			c.VarsRead = true
		case CapVarsWrite:
			c.VarsWrite = true
		case CapCookiesRead:
			c.CookiesRead = true
		case CapCookiesWrite:
			c.CookiesWrite = true
		case CapNetSend:
			c.NetSend = true
		case CapSecretsRead:
			c.SecretsRead = true
		}
	}
	return c
}

// FlowReader reads stored flows and bodies (*store.Store).
type FlowReader interface {
	GetFlow(id int64) (*store.Flow, error)
	OpenBody(hash string) (io.ReadCloser, error)
}

// PMExecutor is the headless collexec.Executor: the goja-backed pm.* sandbox
// wired to the shared pipeline, one instance per step. It mirrors the control
// layer's executor so the CLI and the UI run scripts identically. Only scripts
// the pipeline's trust gate approved reach it, with the collection's granted
// capabilities (empty = nothing).
type PMExecutor struct {
	Coll   store.Collection
	Source collexec.Source
	AI     bool
	EnvUID string
	Layers []varstore.Layer
	Iter   int
	Count  int
	Pipe   *collexec.Pipeline // set after the pipeline is built (nested pm.sendRequest)
	Reg    *redact.Registry
	Flows  FlowReader
	Clock  func() time.Time
	Scope  collexec.ScopeChecker
}

func (e *PMExecutor) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

// Run implements collexec.Executor.
func (e *PMExecutor) Run(ctx context.Context, call collexec.ScriptCall) (collexec.ScriptOutcome, error) {
	sc := call.Ctx
	caps := sandboxCaps(call.Caps)
	in := pmsandbox.Input{
		Phase:  scriptctx.Phase(call.Phase),
		Name:   call.Owner + ":" + call.Name + "/" + call.Phase,
		Script: call.Source,
		Vars:   e.scriptVars(sc),
		Info: scriptctx.Info{EventName: call.Phase, Iteration: sc.Info.Iteration, IterationCount: e.Count,
			RequestName: sc.Info.RequestName, RequestID: sc.Info.RequestID},
		Caps:       caps,
		Clock:      e.now,
		Rand:       rand.Float64,
		Sender:     scriptctx.SendFunc(e.sendFromScript),
		ScopeCheck: e.scopeCheck,
		Scrub:      e.Reg.Mask,
	}
	if caps.CookiesRead {
		in.Cookies = jarCookies(sc.Cookies, e.now())
	}
	var baseline scriptctx.Request
	if sc.Request != nil {
		baseline = toScriptRequest(sc.Request, sc.Info.RequestName, sc.Info.RequestID)
		req := baseline
		in.Request = &req
	}
	if sc.Response != nil {
		in.Response = toScriptResponse(sc.Response)
	}
	out := pmsandbox.Run(ctx, in)
	e.applyOutput(sc, call, baseline, out)
	outcome := collexec.ScriptOutcome{}
	for _, l := range out.Console {
		outcome.Console = append(outcome.Console, jsrt.ConsoleEntry{Level: l.Level, Text: l.Text})
	}
	return outcome, scriptError(out)
}

// scriptError keeps unsupported APIs distinct from failures.
func scriptError(out pmsandbox.Output) error {
	switch out.Status {
	case pmsandbox.StatusUnsupported:
		return &collexec.UnsupportedError{API: strings.Join(out.Unsupported, ", ")}
	case pmsandbox.StatusError:
		msg := "script error"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Kind + ": " + out.Errors[0].Message
		}
		return errors.New(msg)
	}
	return nil
}

func (e *PMExecutor) applyOutput(sc *collexec.ScriptContext, call collexec.ScriptCall, baseline scriptctx.Request, out pmsandbox.Output) {
	caps := sandboxCaps(call.Caps)
	for _, t := range out.Tests {
		sc.AddTest(collexec.TestResult{Name: t.Name, Status: collexec.TestStatus(t.Status), Message: t.Message,
			Expected: t.Expected, Actual: t.Actual, Source: t.Source, DurationMs: t.Duration.Milliseconds(), Owner: call.Owner})
	}
	if caps.VarsWrite {
		for _, ch := range out.Changes {
			switch ch.Op {
			case "set":
				_ = sc.Vars.Set(ch.Scope, ch.Name, valueString(ch.Value))
			case "unset":
				_ = sc.Vars.Unset(ch.Scope, ch.Name)
			case "clear":
				_ = sc.Vars.Clear(ch.Scope)
			}
		}
	}
	if caps.CookiesWrite && sc.Cookies != nil {
		for _, op := range out.CookieOps {
			applyCookieOp(sc.Cookies, op, e.now())
		}
	}
	if call.Phase == string(scriptctx.PhasePreRequest) && out.Request != nil && sc.Request != nil {
		mergeScriptRequest(sc.Request, baseline, *out.Request)
	}
	if out.Flow.Skip {
		sc.SkipRequest()
	}
	if out.Flow.NextSet {
		sc.SetNextRequest(out.Flow.Next)
	}
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func (e *PMExecutor) scriptVars(sc *collexec.ScriptContext) scriptctx.Vars {
	conv := func(scope string) map[string]any {
		m := map[string]any{}
		for k, v := range sc.Vars.ToObject(scope) {
			m[k] = v
		}
		return m
	}
	return scriptctx.Vars{
		Environment: conv(collexec.VarEnvironment), Globals: conv(collexec.VarGlobals),
		Collection: conv(collexec.VarCollection), Local: conv(collexec.VarLocal),
		IterationData: conv(collexec.VarData), Secret: secretNames(e.Layers),
	}
}

func secretNames(layers []varstore.Layer) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range layers {
		for k, v := range l.Vars {
			if v.Secret && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (e *PMExecutor) scopeCheck(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("invalid url")
	}
	if e.Coll.ScopePolicy != store.ScopePolicyOff && e.Scope != nil && !e.Scope.HostInScope(u.Hostname()) {
		return fmt.Errorf("host %s is out of scope", u.Hostname())
	}
	return nil
}

// sendFromScript performs pm.sendRequest through the same Step pipeline as
// every other send. The nested step runs no scripts, so scripts cannot recurse.
func (e *PMExecutor) sendFromScript(ctx context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
	if e.Pipe == nil {
		return scriptctx.SendResponse{}, errors.New("no pipeline for pm.sendRequest")
	}
	item := store.Item{UID: "script-send", CollectionUID: e.Coll.UID, Kind: "request", Name: "pm.sendRequest", Method: r.Method}
	item.URL, _ = json.Marshal(r.URL)
	var hs []map[string]any
	for _, h := range r.Headers {
		hs = append(hs, map[string]any{"key": h.Key, "value": h.Value, "disabled": h.Disabled})
	}
	item.Headers, _ = json.Marshal(hs)
	item.Body, _ = json.Marshal(scriptBodyJSON(r.Body))
	item.Settings = json.RawMessage(`{"unresolved":"literal"}`)
	res, err := e.Pipe.Step(ctx, collexec.StepInput{
		Chain:  collexec.Chain{Collection: e.Coll, Item: item},
		Source: collexec.SourceScript, AI: e.AI, NoScripts: true, EnvUID: e.EnvUID,
	})
	if err != nil {
		return scriptctx.SendResponse{}, err
	}
	if res.Outcome != collexec.OutcomeSent {
		msg := res.Error
		if msg == "" {
			msg = string(res.Outcome) + " " + string(res.BlockReason)
		}
		return scriptctx.SendResponse{}, errors.New(strings.TrimSpace(msg))
	}
	return e.sendResponse(res)
}

func (e *PMExecutor) sendResponse(res *collexec.StepResult) (scriptctx.SendResponse, error) {
	if e.Flows == nil {
		return scriptctx.SendResponse{}, errors.New("no flow reader")
	}
	f, err := e.Flows.GetFlow(res.FlowID)
	if err != nil {
		return scriptctx.SendResponse{}, err
	}
	out := scriptctx.SendResponse{Code: f.Status, Status: http.StatusText(f.Status), ResponseTime: time.Duration(f.DurationMs) * time.Millisecond}
	names := make([]string, 0, len(f.ResHeaders))
	for k := range f.ResHeaders {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, v := range f.ResHeaders[k] {
			out.Headers = append(out.Headers, scriptctx.Header{Key: k, Value: v})
		}
	}
	if f.ResBodyHash != "" {
		if rc, err := e.Flows.OpenBody(f.ResBodyHash); err == nil {
			out.Body, _ = io.ReadAll(io.LimitReader(rc, collexec.MaxResponseBody))
			rc.Close()
		}
	}
	return out, nil
}

// ---- model conversion --------------------------------------------------------

func scriptBodyJSON(b scriptctx.Body) map[string]any {
	m := map[string]any{"mode": b.Mode}
	switch b.Mode {
	case "raw":
		m["raw"] = b.Raw
		if b.Language != "" {
			m["options"] = map[string]any{"raw": map[string]any{"language": b.Language}}
		}
	case "urlencoded":
		m["urlencoded"] = paramsJSON(b.URLEncoded)
	case "formdata":
		m["formdata"] = paramsJSON(b.FormData)
	default:
		if b.Raw != "" {
			m["mode"], m["raw"] = "raw", b.Raw
		}
	}
	return m
}

func paramsJSON(ps []scriptctx.Param) []map[string]any {
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{"key": p.Key, "value": p.Value, "type": p.Type, "disabled": p.Disabled})
	}
	return out
}

func toScriptRequest(m *collexec.RequestModel, name, id string) scriptctx.Request {
	r := scriptctx.Request{Name: name, ID: id, Method: m.Method, URL: scriptURL(m)}
	for _, h := range m.Headers {
		r.Headers = append(r.Headers, scriptctx.Header{Key: h.Key, Value: h.Value, Disabled: h.Disabled})
	}
	b := m.Body
	r.Body = scriptctx.Body{Mode: b.Mode, Raw: b.Raw, Language: b.Language}
	for _, kv := range b.Form {
		r.Body.URLEncoded = append(r.Body.URLEncoded, scriptctx.Param{Key: kv.Key, Value: kv.Value, Disabled: kv.Disabled})
	}
	for _, p := range b.Parts {
		r.Body.FormData = append(r.Body.FormData, scriptctx.Param{Key: p.Key, Value: p.Value, Type: p.Type, Disabled: p.Disabled})
	}
	if m.Auth.Type != "" {
		r.Auth = &scriptctx.Auth{Type: m.Auth.Type, Params: copyStringMap(m.Auth.Fields)}
	}
	return r
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// scriptURL is the URL a script sees: the template URL plus authoritative
// query params.
func scriptURL(m *collexec.RequestModel) string {
	if !m.QueryFromParams {
		return m.URL
	}
	base := m.URL
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	var parts []string
	for _, q := range m.Query {
		if q.Disabled {
			continue
		}
		parts = append(parts, q.Key+"="+q.Value)
	}
	if len(parts) == 0 {
		return base
	}
	return base + "?" + strings.Join(parts, "&")
}

// mergeScriptRequest applies only what the script changed, so a lossless
// request (ordered duplicates, templates) is never rewritten by a no-op script.
func mergeScriptRequest(m *collexec.RequestModel, base, got scriptctx.Request) {
	if got.Method != base.Method {
		m.Method = strings.ToUpper(got.Method)
	}
	if got.URL != base.URL {
		m.URL, m.QueryFromParams, m.Query = got.URL, false, nil
	}
	if !reflect.DeepEqual(got.Headers, base.Headers) {
		m.Headers = nil
		for _, h := range got.Headers {
			m.Headers = append(m.Headers, varstore.KV{Key: h.Key, Value: h.Value, Disabled: h.Disabled})
		}
	}
	if !reflect.DeepEqual(got.Body, base.Body) {
		m.Body.Mode, m.Body.Raw, m.Body.Language = got.Body.Mode, got.Body.Raw, got.Body.Language
		m.Body.Form, m.Body.Parts = nil, nil
		for _, p := range got.Body.URLEncoded {
			m.Body.Form = append(m.Body.Form, varstore.KV{Key: p.Key, Value: p.Value, Disabled: p.Disabled})
		}
		for _, p := range got.Body.FormData {
			t := p.Type
			if t == "" {
				t = "text"
			}
			m.Body.Parts = append(m.Body.Parts, collexec.Part{Key: p.Key, Value: p.Value, Type: t, Disabled: p.Disabled})
		}
	}
	if !reflect.DeepEqual(got.Auth, base.Auth) {
		if got.Auth == nil {
			m.Auth = collexec.AuthModel{Type: "none"}
		} else {
			m.Auth = collexec.AuthModel{Type: got.Auth.Type, Fields: copyStringMap(got.Auth.Params)}
		}
	}
}

func toScriptResponse(r *collexec.ResponseModel) *scriptctx.Response {
	out := &scriptctx.Response{Code: r.Code, Status: r.Status, Body: r.Body, ResponseTime: time.Duration(r.TimeMs) * time.Millisecond}
	for _, h := range r.Headers {
		out.Headers = append(out.Headers, scriptctx.Header{Key: h.Key, Value: h.Value})
	}
	for _, c := range r.Cookies {
		out.Cookies = append(out.Cookies, scriptctx.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, HTTPOnly: c.HttpOnly, Secure: c.Secure})
	}
	return out
}

func jarCookies(j *collexec.Jar, now time.Time) []scriptctx.Cookie {
	if j == nil {
		return nil
	}
	var out []scriptctx.Cookie
	for _, c := range j.List(now) {
		sc := scriptctx.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, HTTPOnly: c.HTTPOnly, Secure: c.Secure}
		if !c.Expires.IsZero() {
			sc.Expires = c.Expires.UTC().Format(time.RFC1123)
		}
		out = append(out, sc)
	}
	return out
}

func applyCookieOp(j *collexec.Jar, op scriptctx.CookieOp, now time.Time) {
	switch op.Op {
	case "set":
		u, err := url.Parse(op.URL)
		if err != nil || u.Hostname() == "" {
			return
		}
		j.Store(u, []*http.Cookie{{Name: op.Cookie.Name, Value: op.Cookie.Value, Domain: op.Cookie.Domain,
			Path: op.Cookie.Path, HttpOnly: op.Cookie.HTTPOnly, Secure: op.Cookie.Secure}}, now)
	case "clear":
		if op.Cookie.Name == "" {
			j.Clear()
		}
	}
}
