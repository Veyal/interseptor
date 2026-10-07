package collexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/codec"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// stepRun is the working state of one Step.
type stepRun struct {
	p       *Pipeline
	in      StepInput
	res     *StepResult
	vars    *Vars
	jar     *Jar
	trusted map[string]bool // script hash -> trusted
	caps    []string
	flowIDs []int64
}

// Step runs one request through the whole pipeline. The returned error is
// reserved for misuse (no sender, no item); every runtime outcome, including
// blocks and failures, is reported in the StepResult.
func (p *Pipeline) Step(ctx context.Context, in StepInput) (*StepResult, error) {
	if p == nil || p.Sender == nil {
		return nil, errNoSender
	}
	if in.Chain.Item.UID == "" {
		return nil, errors.New("collexec: step has no item")
	}
	if p.Jars == nil || p.Registry == nil || p.Clock == nil {
		return nil, errors.New("collexec: pipeline not initialised (use NewPipeline)")
	}
	ctx = ctxOrBackground(ctx)
	x := &stepRun{p: p, in: in, res: &StepResult{Outcome: OutcomeError}}
	x.vars = newVars(in.Layers, in.Local, p.Registry)
	x.vars.Stack().RegisterSecrets(p.Registry)
	x.jar = p.Jars.For(in.Chain.Collection.UID, in.EnvUID, in.Identity)
	x.caps = capSet(in.Chain.Collection.Caps)
	defer func() { x.finish() }()

	settings := in.Chain.settings()
	model := in.Chain.model()
	pre := in.Chain.scripts(ListenPre)
	tests := in.Chain.scripts(ListenTest)
	if !x.gate(pre, tests) {
		return x.res, nil
	}

	sc := &ScriptContext{
		Info:    ScriptInfo{EventName: ListenPre, RequestName: in.Chain.Item.Name, RequestID: in.Chain.Item.UID, Iteration: in.Iteration, RunID: in.RunID},
		Request: model, Vars: x.vars, Cookies: x.jar,
	}
	if !x.runScripts(ctx, ListenPre, pre, sc) {
		return x.res, nil
	}
	x.applyFlow(sc)
	if sc.skip {
		x.res.Outcome = OutcomeSkipped
		return x.res, nil
	}

	b, templateHash, ok := x.resolve(sc.Request, settings)
	if !ok {
		return x.res, nil
	}
	if !x.prepare(b, settings) {
		return x.res, nil
	}
	policy := scopePolicy(in)
	if !x.applyAuth(ctx, b, sc.Request, policy) {
		return x.res, nil
	}
	x.res.Method, x.res.URL = b.Method, b.URL

	req, ok := x.buildSend(b, settings, policy, templateHash)
	if !ok {
		return x.res, nil
	}
	flow, ok := x.send(ctx, req, settings, policy)
	if !ok {
		return x.res, nil
	}

	x.res.Outcome = OutcomeSent
	resp := x.responseModel(flow)
	x.res.Response = &ResponseSummary{
		Status: flow.Status, StatusText: http.StatusText(flow.Status),
		ContentType: firstHeader(flow.ResHeaders, "Content-Type"), Size: flow.ResLen, TimeMs: flow.DurationMs,
		Error: flow.Error,
	}
	if flow.Error != "" {
		x.res.Outcome = OutcomeError
		x.res.Error = flow.Error
		if strings.Contains(flow.Error, sender.ErrGuardBlocked.Error()) {
			x.res.Outcome, x.res.BlockReason = OutcomeBlocked, BlockScope
		}
		return x.res, nil
	}

	tsc := &ScriptContext{
		Info:    ScriptInfo{EventName: ListenTest, RequestName: in.Chain.Item.Name, RequestID: in.Chain.Item.UID, Iteration: in.Iteration, RunID: in.RunID},
		Request: wireModel(b), Response: resp, Vars: x.vars, Cookies: x.jar,
	}
	x.runScripts(ctx, ListenTest, tests, tsc)
	x.applyFlow(tsc)
	x.res.Tests = append(x.res.Tests, evalAssertions(in.Chain.Item.Assertions, resp)...)
	return x.res, nil
}

// gate applies the trust gate up front: untrusted scripts will not run, and a
// headless caller can refuse to run at all.
func (x *stepRun) gate(groups ...[]scriptRef) bool {
	x.trusted = map[string]bool{}
	quarantined := false
	for _, g := range groups {
		for _, s := range g {
			if _, seen := x.trusted[s.Hash]; seen {
				continue
			}
			ok := false
			if !x.in.NoScripts && x.p.Trust != nil {
				if t, err := x.p.Trust.IsScriptTrusted(x.in.Chain.Collection.UID, s.Hash); err == nil {
					ok = t
				}
			}
			x.trusted[s.Hash] = ok
			if !ok && !x.in.NoScripts {
				quarantined = true
			}
		}
	}
	if quarantined && x.in.FailOnQuarantine {
		x.res.Outcome, x.res.BlockReason = OutcomeBlocked, BlockQuarantined
		x.res.Error = "collection has untrusted scripts; trust them in the UI or run with --no-scripts"
		for _, g := range groups {
			for _, s := range g {
				if !x.trusted[s.Hash] {
					x.note(s, "quarantined")
				}
			}
		}
		return false
	}
	return true
}

func (x *stepRun) note(s scriptRef, reason string) {
	x.res.Scripts = append(x.res.Scripts, ScriptNote{Owner: s.Owner, Name: s.Name, Listen: s.Listen, Hash: s.Hash, Reason: reason})
}

// runScripts executes a phase's scripts outer to inner. It returns false when
// the step must stop (a pre-request script failed).
func (x *stepRun) runScripts(ctx context.Context, listen string, refs []scriptRef, sc *ScriptContext) bool {
	for _, s := range refs {
		switch {
		case x.in.NoScripts:
			x.note(s, "scripts disabled")
			continue
		case !x.trusted[s.Hash]:
			x.note(s, "quarantined: not trusted")
			continue
		case x.p.Exec == nil:
			x.note(s, ErrNoExecutor.Error())
			continue
		}
		out, err := x.callExecutor(ctx, listen, s, sc)
		for _, c := range out.Console {
			x.res.Console = append(x.res.Console, ConsoleLine{Owner: s.Owner, Level: c.Level, Text: c.Text})
		}
		for _, t := range sc.tests {
			if t.Owner == "" {
				t.Owner = s.Owner
			}
			x.res.Tests = append(x.res.Tests, t)
		}
		sc.tests = nil
		if err == nil {
			if listen == ListenPre && sc.skip {
				return true
			}
			continue
		}
		var ue *UnsupportedError
		if errors.As(err, &ue) {
			x.note(s, "unsupported: "+ue.API)
			if listen == ListenTest {
				x.res.Tests = append(x.res.Tests, TestResult{Name: s.Owner + " test script", Status: TestUnsupported, Message: ue.Error(), Owner: s.Owner})
				continue
			}
		} else {
			x.note(s, "error: "+scriptErr(err))
			if listen == ListenTest {
				x.res.Tests = append(x.res.Tests, TestResult{Name: s.Owner + " test script", Status: TestError, Message: scriptErr(err), Owner: s.Owner})
				continue
			}
		}
		x.res.Outcome = OutcomeError
		x.res.Error = fmt.Sprintf("%s pre-request script failed: %s", s.Owner, scriptErr(err))
		return false
	}
	return true
}

func (x *stepRun) callExecutor(ctx context.Context, listen string, s scriptRef, sc *ScriptContext) (out ScriptOutcome, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("script engine panic: %v", r)
		}
	}()
	return x.p.Exec.Run(ctx, ScriptCall{Phase: listen, Owner: s.Owner, Name: s.Name, Source: s.Source, Hash: s.Hash, Ctx: sc, Caps: x.caps})
}

func (x *stepRun) applyFlow(sc *ScriptContext) {
	if sc.hasNext {
		x.res.Flow = FlowControl{HasNext: true, NextRequest: sc.next}
	}
}

// resolve substitutes variables over the (script-mutated) structured request
// and assembles the wire request.
func (x *stepRun) resolve(m *RequestModel, settings Settings) (*built, string, bool) {
	hash := templateHash(m)
	r := varstore.New(varstore.Options{
		Clock: x.p.Clock, Rand: x.p.Rand, Policy: unresolvedPolicy(settings.Unresolved), Registry: x.p.Registry,
	})
	resolved, rr := r.ResolveRequest(toVarRequest(m), x.vars.Stack())
	x.res.Uses, x.res.Unresolved = rr.Uses, rr.Unresolved
	x.res.Warnings = append(x.res.Warnings, rr.Problems...)
	if rr.Err != nil {
		if errors.Is(rr.Err, varstore.ErrUnresolved) {
			x.res.Outcome, x.res.BlockReason = OutcomeBlocked, BlockUnresolved
		}
		x.res.Error = rr.Err.Error()
		return nil, "", false
	}
	b, err := assemble(m, resolved, x.p.Auth != nil)
	if err != nil {
		x.res.Error = err.Error()
		return nil, "", false
	}
	x.res.Warnings = append(x.res.Warnings, b.Warnings...)
	x.res.Applied = append(x.res.Applied, b.Applied...)
	return b, hash, true
}

func templateHash(m *RequestModel) string {
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// prepare attaches cookies and applies the codec.
func (x *stepRun) prepare(b *built, settings Settings) bool {
	u, _ := url.Parse(b.URL)
	if !hasHeader(b.Headers, "Cookie") {
		if c := x.jar.Header(u, x.p.Clock()); c != "" {
			b.Headers = append(b.Headers, varstore.KV{Key: "Cookie", Value: c})
		}
	}
	if x.p.Encoder == nil || strings.EqualFold(settings.Codec, "off") || b.BodyText == "" && settings.Codec == "" {
		return true
	}
	hdrs := map[string][]string{}
	for _, h := range b.Headers {
		hdrs[http.CanonicalHeaderKey(h.Key)] = append(hdrs[http.CanonicalHeaderKey(h.Key)], h.Value)
	}
	body, applied, err := x.p.Encoder.EncodeRequest(settings.Codec, CodecRequest{
		Method: b.Method, Scheme: u.Scheme, Host: u.Hostname(), Port: portOf(u), Path: u.RequestURI(),
		Headers: hdrs, Body: b.BodyText,
	})
	if err != nil {
		x.res.Error = "codec: " + err.Error()
		return false
	}
	if applied != "" {
		b.Body = []byte(body)
		x.res.Applied = append(x.res.Applied, "codec:"+applied)
	}
	return true
}

// check enforces own-listener refusal, the base-target pin and scope for one
// destination. It returns "" to proceed, or a block reason with a message.
func (x *stepRun) check(u *url.URL, policy string) (BlockReason, string) {
	return x.checkDest(u, policy, x.in.EnvPin)
}

// checkDest is check with an explicit base-target pin ("" for destinations the
// pin does not describe, such as an identity provider's token endpoint).
func (x *stepRun) checkDest(u *url.URL, policy, pin string) (BlockReason, string) {
	host, port := u.Hostname(), portOf(u)
	if x.p.isOwn(host, port) {
		return BlockOwn, fmt.Sprintf("refusing to send to the tool's own listener %s:%d", host, port)
	}
	if policy == store.ScopePolicyOff {
		return "", ""
	}
	var reason BlockReason
	var msg string
	switch {
	case pin != "" && !pinMatches(pin, u):
		reason, msg = BlockPin, fmt.Sprintf("host %s does not match the environment's base target pin", host)
	case !x.p.urlInScope(u):
		reason, msg = BlockScope, fmt.Sprintf("host %s is out of scope", host)
	default:
		return "", ""
	}
	if policy == store.ScopePolicyWarn {
		x.res.Warnings = append(x.res.Warnings, msg+" (warn policy: sent anyway)")
		return "", ""
	}
	return reason, msg
}

func (x *stepRun) buildSend(b *built, settings Settings, policy, tmplHash string) (sender.Request, bool) {
	u, _ := url.Parse(b.URL)
	if reason, msg := x.check(u, policy); reason != "" {
		x.res.Outcome, x.res.BlockReason, x.res.Error = OutcomeBlocked, reason, msg
		return sender.Request{}, false
	}
	useSession := settings.UseSession != nil && *settings.UseSession
	flags := store.FlagCollection
	if x.in.AI {
		flags |= store.FlagAI
	}
	opts := &sender.SendOptions{
		Timeout:   DefaultTimeout,
		VerifyTLS: settings.VerifyTLS != nil && *settings.VerifyTLS,
		Guard:     x.p.guardFor(policy, u.Hostname()),
		Meta:      sender.Meta{RunID: x.in.RunID, ItemID: x.in.Chain.Item.UID, Iteration: x.in.Iteration, Phase: "main"},
	}
	if settings.TimeoutMs != nil {
		opts.Timeout = 0
		if *settings.TimeoutMs > 0 {
			opts.Timeout = msDuration(*settings.TimeoutMs)
		}
	}
	opts.OnFlow = func(f *store.Flow, m sender.Meta) { x.onFlow(f, tmplHash) }
	req := sender.Request{
		Method: b.Method, URL: b.URL, Body: b.Body, Flags: flags, NoSession: !useSession, Options: opts,
	}
	setHeaders(&req, b.Headers, settings.RawHeaders != nil && *settings.RawHeaders)
	return req, true
}

// setHeaders uses the ordered raw wire path when the caller asked for it or
// when header names repeat (the map form cannot keep duplicate order).
func setHeaders(req *sender.Request, hs []varstore.KV, forceRaw bool) {
	seen := map[string]bool{}
	dup := false
	for _, h := range hs {
		k := strings.ToLower(h.Key)
		dup = dup || seen[k]
		seen[k] = true
	}
	if forceRaw || dup {
		for _, h := range hs {
			req.Options.RawHeaders = append(req.Options.RawHeaders, sender.RawHeader{Name: h.Key, Value: h.Value})
		}
		return
	}
	req.Headers = map[string][]string{}
	for _, h := range hs {
		if strings.EqualFold(h.Key, "Host") {
			req.Host = h.Value
			continue
		}
		req.Headers[h.Key] = append(req.Headers[h.Key], h.Value)
	}
}

// onFlow runs inside the sender after each flow is persisted. Capture is best
// effort: nothing in here may fail or panic into the send.
func (x *stepRun) onFlow(f *store.Flow, tmplHash string) {
	defer func() { _ = recover() }()
	if f == nil || f.ID == 0 {
		return
	}
	parent := x.in.ParentFlowID
	if n := len(x.flowIDs); n > 0 {
		parent = x.flowIDs[n-1]
	}
	x.flowIDs = append(x.flowIDs, f.ID)
	if x.p.Flows == nil {
		return
	}
	_ = x.p.Flows.PutFlowCtx(store.FlowCtx{
		FlowID: f.ID, RunID: x.in.RunID, ItemUID: x.in.Chain.Item.UID, Iteration: x.in.Iteration,
		Phase: "main", ParentFlowID: parent, EnvUID: x.in.EnvUID, Identity: x.in.Identity, TemplateHash: tmplHash,
	})
}

// send issues the request and follows redirects manually so scope and the
// own-listener rule are re-checked per hop.
func (x *stepRun) send(ctx context.Context, req sender.Request, settings Settings, policy string) (*store.Flow, bool) {
	follow := settings.FollowRedirects != nil && *settings.FollowRedirects
	max := settings.MaxRedirects
	if max <= 0 {
		max = 10
	}
	req.Context = ctx
	var flow *store.Flow
	for hop := 0; ; hop++ {
		var err error
		flow, err = x.p.Sender.Send(req)
		if err != nil {
			x.res.Error = err.Error()
			return nil, false
		}
		if flow == nil {
			x.res.Error = "sender returned no flow"
			return nil, false
		}
		x.storeCookies(req.URL, flow)
		if !follow || hop >= max || flow.Error != "" {
			break
		}
		next, ok := nextHop(req, flow)
		if !ok {
			break
		}
		nu, _ := url.Parse(next.URL)
		if reason, msg := x.check(nu, policy); reason != "" {
			x.res.Warnings = append(x.res.Warnings, "redirect not followed: "+msg)
			break
		}
		if next.Options != nil {
			next.Options.Guard = x.p.guardFor(policy, nu.Hostname())
		}
		req = next
	}
	if len(x.flowIDs) == 0 && flow.ID != 0 {
		x.flowIDs = append(x.flowIDs, flow.ID)
	}
	return flow, true
}

func (x *stepRun) storeCookies(rawURL string, f *store.Flow) {
	u, err := url.Parse(rawURL)
	if err != nil || len(f.ResHeaders) == 0 {
		return
	}
	cs := (&http.Response{Header: http.Header(f.ResHeaders)}).Cookies()
	x.jar.Store(u, cs, x.p.Clock())
}

// nextHop builds the follow-up request for a 3xx flow (same rules as the
// sender's own redirect handling: method downgrade, credentials dropped across
// hosts).
func nextHop(r sender.Request, f *store.Flow) (sender.Request, bool) {
	switch f.Status {
	case 301, 302, 303, 307, 308:
	default:
		return r, false
	}
	loc := http.Header(f.ResHeaders).Get("Location")
	base, err := url.Parse(r.URL)
	if loc == "" || err != nil {
		return r, false
	}
	target, err := base.Parse(loc)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return r, false
	}
	n := r
	n.URL, n.Host = target.String(), ""
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	dropBody := f.Status == 303 && method != http.MethodHead ||
		(f.Status == 301 || f.Status == 302) && method == http.MethodPost
	if dropBody {
		n.Method, n.Body = http.MethodGet, nil
	}
	cross := !strings.EqualFold(base.Host, target.Host)
	skip := func(k string) bool {
		c := http.CanonicalHeaderKey(k)
		if c == "Host" || dropBody && (c == "Content-Length" || c == "Content-Type" || c == "Transfer-Encoding") {
			return true
		}
		return cross && (c == "Authorization" || c == "Cookie" || c == "Proxy-Authorization")
	}
	n.Headers = nil
	if r.Headers != nil {
		n.Headers = map[string][]string{}
		for k, vs := range r.Headers {
			if !skip(k) {
				n.Headers[k] = append([]string(nil), vs...)
			}
		}
	}
	if r.Options != nil {
		oc := *r.Options
		oc.RawHeaders = nil
		for _, h := range r.Options.RawHeaders {
			if !skip(h.Name) {
				oc.RawHeaders = append(oc.RawHeaders, h)
			}
		}
		n.Options = &oc
	}
	return n, true
}

// responseModel reads and decompresses the final response for scripts and
// assertions. Reading is best effort: a missing body yields an empty one.
func (x *stepRun) responseModel(f *store.Flow) *ResponseModel {
	m := &ResponseModel{
		Code: f.Status, Status: http.StatusText(f.Status), TimeMs: f.DurationMs, Size: f.ResLen,
		FlowID: f.ID, HTTPVersion: f.HTTPVersion,
		Cookies: (&http.Response{Header: http.Header(f.ResHeaders)}).Cookies(),
	}
	keys := make([]string, 0, len(f.ResHeaders))
	for k := range f.ResHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range f.ResHeaders[k] {
			m.Headers = append(m.Headers, varstore.KV{Key: k, Value: v})
		}
	}
	if x.p.Bodies == nil || f.ResBodyHash == "" {
		return m
	}
	rc, err := x.p.Bodies.OpenBody(f.ResBodyHash)
	if err != nil {
		return m
	}
	defer rc.Close()
	body, _ := io.ReadAll(io.LimitReader(rc, MaxResponseBody+1))
	if len(body) > MaxResponseBody {
		body, m.BodyTruncated = body[:MaxResponseBody], true
	}
	if ce := firstHeader(f.ResHeaders, "Content-Encoding"); ce != "" {
		if out, ok, trunc := codec.DecompressBodyLimit(ce, body, MaxResponseBody); ok {
			body, m.BodyTruncated = out, m.BodyTruncated || trunc
		}
	}
	m.Body = body
	return m
}

func firstHeader(h map[string][]string, name string) string {
	return http.Header(h).Get(name)
}

// wireModel is the request as sent, handed to test scripts as pm.request.
func wireModel(b *built) *RequestModel {
	m := &RequestModel{Method: b.Method, URL: b.URL, Body: BodyModel{Mode: BodyRaw, Raw: b.BodyText}}
	for _, h := range b.Headers {
		m.Headers = append(m.Headers, varstore.KV{Key: h.Key, Value: h.Value})
	}
	return m
}

// finish fills in the result's flow ids and variable changes and masks every
// text field. It runs on every exit path.
func (x *stepRun) finish() {
	r := x.res
	r.FlowIDs = x.flowIDs
	if n := len(x.flowIDs); n > 0 {
		r.FlowID = x.flowIDs[n-1]
	}
	r.VarChanges = x.vars.Changes()
	mask := x.p.Registry.Mask
	r.URL, r.Error = mask(r.URL), mask(r.Error)
	for i, w := range r.Warnings {
		r.Warnings[i] = mask(w)
	}
	for i := range r.Console {
		r.Console[i].Text = mask(r.Console[i].Text)
	}
	for i := range r.Tests {
		t := &r.Tests[i]
		t.Name, t.Expected, t.Actual, t.Message = mask(t.Name), mask(t.Expected), mask(t.Actual), mask(t.Message)
	}
	for i := range r.Scripts {
		r.Scripts[i].Reason = mask(r.Scripts[i].Reason)
	}
	for i := range r.VarChanges {
		c := &r.VarChanges[i]
		if !c.Secret {
			c.Display = mask(c.Display)
		}
	}
	if r.Response != nil {
		r.Response.Error = mask(r.Response.Error)
	}
}
