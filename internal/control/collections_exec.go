package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// ownListeners returns the ports and addresses of Interseptor's own
// listeners (proxy and control) so collection sends can never reach them,
// whatever the scope policy: every local interface address is "own".
func (h *Hub) ownListeners() ([]int, []net.IP) {
	var ports []int
	seen := map[int]bool{}
	addrs := append([]string{h.GetSelfAddr()}, h.currentProxyAddrs()...)
	for _, a := range addrs {
		_, p, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && !seen[n] {
			seen[n] = true
			ports = append(ports, n)
		}
	}
	var ips []net.IP
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				ips = append(ips, ipn.IP)
			}
		}
	}
	return ports, ips
}

// stepEnv is everything one step's script engine needs beyond the call.
type stepEnv struct {
	c      *collectionsAPI
	coll   store.Collection
	source collexec.Source
	ai     bool
	envUID string
	layers []varstore.Layer
	iter   int
	count  int
	pipe   *collexec.Pipeline
}

// pipeline builds the shared execution pipeline for one step, wired to the
// real script engine. Jars and the masking registry persist across steps.
func (c *collectionsAPI) pipeline(env *stepEnv) *collexec.Pipeline {
	ports, ips := c.h.ownListeners()
	p := collexec.NewPipeline(collexec.Pipeline{
		Sender: c.h.snd, Exec: env, Scope: c.h.sc, Trust: c.h.st, Flows: c.h.st, Bodies: c.h.st,
		Jars: c.jars, Registry: c.reg, OwnPorts: ports, OwnIPs: ips,
	})
	env.pipe = p
	return p
}

// Run implements collexec.Executor with the real goja-backed pm.* sandbox. It
// runs only scripts the pipeline's trust gate already approved, with the
// collection's granted capabilities (empty = nothing).
func (e *stepEnv) Run(ctx context.Context, call collexec.ScriptCall) (collexec.ScriptOutcome, error) {
	sc := call.Ctx
	caps := sandboxCaps(call.Caps)
	in := pmsandbox.Input{
		Phase:  scriptctx.Phase(call.Phase),
		Name:   call.Owner + ":" + call.Name + "/" + call.Phase,
		Script: call.Source,
		Vars:   e.scriptVars(sc),
		Info: scriptctx.Info{EventName: call.Phase, Iteration: sc.Info.Iteration, IterationCount: e.count,
			RequestName: sc.Info.RequestName, RequestID: sc.Info.RequestID},
		Caps:       caps,
		Clock:      e.c.pipelineClock,
		Rand:       rand.Float64,
		Sender:     scriptctx.SendFunc(e.sendFromScript),
		ScopeCheck: e.scopeCheck,
		Scrub:      e.c.reg.Mask,
	}
	if caps.CookiesRead {
		in.Cookies = jarCookies(sc.Cookies, e.c.pipelineClock())
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

func (c *collectionsAPI) pipelineClock() time.Time { return time.Now() }

// scriptError turns a sandbox verdict into the pipeline's error vocabulary:
// unsupported APIs stay distinct from failures.
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

// applyOutput commits a script's effects into the pipeline context.
func (e *stepEnv) applyOutput(sc *collexec.ScriptContext, call collexec.ScriptCall, baseline scriptctx.Request, out pmsandbox.Output) {
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
			applyCookieOp(sc.Cookies, op, e.c.pipelineClock())
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

func (e *stepEnv) scriptVars(sc *collexec.ScriptContext) scriptctx.Vars {
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
		IterationData: conv(collexec.VarData), Secret: secretNames(e.layers),
	}
}

func (e *stepEnv) scopeCheck(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("invalid url")
	}
	if e.c.h.targetsOwnListener(raw) {
		return errors.New("refusing to reach Interseptor's own listener")
	}
	if e.coll.ScopePolicy != store.ScopePolicyOff && !e.c.h.sc.HostInScope(u.Hostname()) {
		return fmt.Errorf("host %s is out of scope", u.Hostname())
	}
	return nil
}

// sendFromScript performs pm.sendRequest through the same Step pipeline as
// every other send: scope guard, own-listener refusal, dial guard, capture.
// The nested step runs no scripts, so scripts cannot recurse.
func (e *stepEnv) sendFromScript(ctx context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
	item := store.Item{UID: "script-send", CollectionUID: e.coll.UID, Kind: "request", Name: "pm.sendRequest", Method: r.Method}
	item.URL, _ = json.Marshal(r.URL)
	var hs []map[string]any
	for _, h := range r.Headers {
		hs = append(hs, map[string]any{"key": h.Key, "value": h.Value, "disabled": h.Disabled})
	}
	item.Headers, _ = json.Marshal(hs)
	item.Body, _ = json.Marshal(scriptBodyJSON(r.Body))
	item.Settings = json.RawMessage(`{"unresolved":"literal"}`)
	res, err := e.pipe.Step(ctx, collexec.StepInput{
		Chain:  collexec.Chain{Collection: e.coll, Item: item},
		Source: collexec.SourceScript, AI: e.ai, NoScripts: true, EnvUID: e.envUID,
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
	return e.c.sendResponse(res)
}

func (c *collectionsAPI) sendResponse(res *collexec.StepResult) (scriptctx.SendResponse, error) {
	f, err := c.h.st.GetFlow(res.FlowID)
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
		if rc, err := c.h.st.OpenBody(f.ResBodyHash); err == nil {
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

// ---- variable persistence ----------------------------------------------------

// varOverlay carries script variable writes between the steps of one run so
// token chains work even when nothing is persisted.
type varOverlay struct {
	set   map[string]map[string]string // scope -> key -> value
	unset map[string]map[string]bool
}

func newOverlay() *varOverlay {
	return &varOverlay{set: map[string]map[string]string{}, unset: map[string]map[string]bool{}}
}

func (o *varOverlay) record(changes []collexec.VarChange) {
	for _, ch := range changes {
		if ch.Scope == collexec.VarLocal {
			continue
		}
		if o.set[ch.Scope] == nil {
			o.set[ch.Scope], o.unset[ch.Scope] = map[string]string{}, map[string]bool{}
		}
		if ch.Unset {
			delete(o.set[ch.Scope], ch.Key)
			o.unset[ch.Scope][ch.Key] = true
		} else {
			o.set[ch.Scope][ch.Key] = ch.Value
			delete(o.unset[ch.Scope], ch.Key)
		}
	}
}

var overlayScopes = map[string]varstore.Scope{
	collexec.VarEnvironment: varstore.ScopeEnvironment,
	collexec.VarGlobals:     varstore.ScopeGlobal,
	collexec.VarCollection:  varstore.ScopeCollection,
}

// apply layers the overlay onto freshly built layers.
func (o *varOverlay) apply(layers []varstore.Layer) []varstore.Layer {
	for name, sc := range overlayScopes {
		for k, v := range o.set[name] {
			layers = setInLayers(layers, sc, k, v)
		}
		for k := range o.unset[name] {
			for _, l := range layers {
				if l.Scope == sc {
					delete(l.Vars, k)
				}
			}
		}
	}
	return layers
}

func setInLayers(layers []varstore.Layer, sc varstore.Scope, key, value string) []varstore.Layer {
	for i := range layers {
		if layers[i].Scope == sc {
			v := layers[i].Vars[key]
			v.Value = value
			layers[i].Vars[key] = v
			return layers
		}
	}
	return append(layers, varstore.Layer{Scope: sc, Name: sc.String(), Vars: map[string]varstore.Var{key: {Value: value}}})
}

// commitChanges writes script variable changes as local current values.
// Initial values are never touched. Writes with no owner (no environment
// selected) are skipped and reported.
func (c *collectionsAPI) commitChanges(coll store.Collection, envUID string, changes []collexec.VarChange) (skipped []string) {
	for _, ch := range changes {
		var kind, uid string
		switch ch.Scope {
		case collexec.VarEnvironment:
			kind, uid = store.VarOwnerEnvironment, envUID
		case collexec.VarCollection:
			kind, uid = store.VarOwnerCollection, coll.UID
		case collexec.VarGlobals:
			kind, uid = store.VarOwnerEnvironment, c.globalsUID(coll.UID)
		default:
			continue
		}
		if uid == "" || ch.Unset {
			skipped = append(skipped, ch.Scope+"."+ch.Key)
			continue
		}
		if _, err := c.setCurrentValue(kind, uid, ch.Key, ch.Value, "script", false); err != nil {
			skipped = append(skipped, ch.Scope+"."+ch.Key)
		}
	}
	return skipped
}

// globalsUID finds (or creates) the globals holder for a collection.
func (c *collectionsAPI) globalsUID(collUID string) string {
	envs, err := c.h.st.ListEnvironments()
	if err != nil {
		return ""
	}
	for _, e := range envs {
		if e.Kind == "globals" && (e.CollectionUID == "" || e.CollectionUID == collUID) {
			return e.UID
		}
	}
	e, err := c.h.st.CreateEnvironment(store.Environment{Name: "Globals", Kind: "globals"})
	if err != nil {
		return ""
	}
	return e.UID
}
