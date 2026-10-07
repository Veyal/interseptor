package collexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collauth"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// authTimeout bounds one token-endpoint or digest-probe exchange.
const authTimeout = 30 * time.Second

// maxAuthResponse bounds a token endpoint response body.
const maxAuthResponse = 1 << 20

type authCtxKey struct{}

// authEnv is the step a token request belongs to: its scope policy, source and
// own-listener rules apply to the identity provider exactly as to the API.
type authEnv struct {
	x      *stepRun
	policy string
}

// AuthContext binds ctx to a step so token requests made by the collauth
// Manager during it (StepDoer) are scope-checked, dial-guarded and captured as
// collection flows. Hosts use it for the OAuth2 authorization-code callback,
// which happens outside any Step.
func (p *Pipeline) AuthContext(ctx context.Context, in StepInput) context.Context {
	x := &stepRun{p: p, in: in, res: &StepResult{}}
	return context.WithValue(ctxOrBackground(ctx), authCtxKey{}, &authEnv{x: x, policy: scopePolicy(in)})
}

// StepDoer is the collauth.Doer for collections: it sends through the scope-
// guarded sender of the step bound by AuthContext. Without a bound step it
// refuses to dial (fail closed) instead of falling back to a bare http.Client.
type StepDoer struct{}

// Do implements collauth.Doer.
func (StepDoer) Do(req *http.Request) (*http.Response, error) {
	env, _ := req.Context().Value(authCtxKey{}).(*authEnv)
	if env == nil {
		return nil, errors.New("auth request refused: no active collection step")
	}
	return env.x.doAuthRequest(req, env.policy)
}

func (x *stepRun) doAuthRequest(req *http.Request, policy string) (*http.Response, error) {
	u := req.URL
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("auth request: invalid URL")
	}
	// The base-target pin describes the API, not the identity provider.
	if reason, msg := x.checkDest(u, policy, ""); reason != "" {
		return nil, errors.New("auth request blocked: " + msg)
	}
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(io.LimitReader(req.Body, maxAuthResponse)); err != nil {
			return nil, err
		}
	}
	flags := store.FlagCollection
	if x.in.AI {
		flags |= store.FlagAI
	}
	hdr := map[string][]string{}
	for k, vs := range req.Header {
		hdr[k] = append([]string(nil), vs...)
	}
	meta := sender.Meta{RunID: x.in.RunID, ItemID: x.in.Chain.Item.UID, Iteration: x.in.Iteration, Phase: "auth"}
	flow, err := x.p.Sender.Send(sender.Request{
		Method: req.Method, URL: u.String(), Headers: hdr, Body: body, Flags: flags, NoSession: true, Context: req.Context(),
		Options: &sender.SendOptions{Timeout: authTimeout, Guard: x.p.guardFor(policy, u.Hostname()), Meta: meta,
			OnFlow: func(f *store.Flow, m sender.Meta) { x.onAuthFlow(f) }},
	})
	if err != nil {
		return nil, err
	}
	if flow == nil || flow.Error != "" {
		msg := "no response"
		if flow != nil {
			msg = flow.Error
		}
		return nil, errors.New(msg)
	}
	resp := &http.Response{StatusCode: flow.Status, Status: fmt.Sprintf("%d %s", flow.Status, http.StatusText(flow.Status)),
		Header: http.Header(flow.ResHeaders), Request: req, Body: io.NopCloser(bytes.NewReader(nil))}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	if flow.ResBodyHash != "" && x.p.Bodies != nil {
		if rc, err := x.p.Bodies.OpenBody(flow.ResBodyHash); err == nil {
			b, _ := io.ReadAll(io.LimitReader(rc, maxAuthResponse))
			rc.Close()
			resp.Body = io.NopCloser(bytes.NewReader(b))
		}
	}
	return resp, nil
}

// onAuthFlow records flow context for an auth exchange (best effort).
func (x *stepRun) onAuthFlow(f *store.Flow) {
	defer func() { _ = recover() }()
	if f == nil || f.ID == 0 || x.p.Flows == nil {
		return
	}
	_ = x.p.Flows.PutFlowCtx(store.FlowCtx{FlowID: f.ID, RunID: x.in.RunID, ItemUID: x.in.Chain.Item.UID, Iteration: x.in.Iteration,
		Phase: "auth", EnvUID: x.in.EnvUID, Identity: x.in.Identity})
}

// AuthCacheKey identifies an OAuth2 token slot: the same auth configuration in
// the same collection, environment and identity shares one token.
func AuthCacheKey(coll, envUID, identity string, a AuthModel) string {
	names := make([]string, 0, len(a.Fields))
	for k := range a.Fields {
		names = append(names, k)
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte(a.Type))
	for _, k := range names {
		h.Write([]byte{0})
		h.Write([]byte(k + "=" + a.Fields[k]))
	}
	return coll + "|" + envUID + "|" + identity + "|" + hex.EncodeToString(h.Sum(nil))[:12]
}

// ResolveAuth returns the effective auth of a request item with its variables
// resolved (unresolved variables are an error), plus the token cache key. The
// OAuth2 authorization-code flow needs it outside a Step.
func ResolveAuth(in StepInput, reg *redact.Registry) (collauth.Config, string, error) {
	m := in.Chain.model()
	stack := newVars(in.Layers, in.Local, reg, nil).Stack()
	r := varstore.New(varstore.Options{Policy: varstore.PolicyBlock, Registry: reg})
	cfg := collauth.Config{Type: m.Auth.Type, Fields: map[string]string{}}
	for k, v := range m.Auth.Fields {
		res := r.Resolve(v, stack)
		if res.Err != nil {
			return cfg, "", fmt.Errorf("auth field %s: %w", k, res.Err)
		}
		cfg.Fields[k] = res.Value
	}
	return cfg, AuthCacheKey(in.Chain.Collection.UID, in.EnvUID, in.Identity, m.Auth), nil
}

// applyAuth runs the collauth suite over the final wire request (after
// scripts, variables, cookies and the codec) and merges what it added.
func (x *stepRun) applyAuth(ctx context.Context, b *built, m *RequestModel, policy string) bool {
	if x.p.Auth == nil {
		return true
	}
	switch strings.ToLower(b.AuthType) {
	case "", "none", "inherit":
		return true
	}
	hdr := http.Header{}
	for _, h := range b.Headers {
		hdr.Add(h.Key, h.Value)
	}
	req := &collauth.Request{Method: b.Method, URL: b.URL, Header: hdr, Body: b.Body}
	key := AuthCacheKey(x.in.Chain.Collection.UID, x.in.EnvUID, x.in.Identity, m.Auth)
	actx := context.WithValue(ctx, authCtxKey{}, &authEnv{x: x, policy: policy})
	res, err := x.p.Auth.Apply(actx, key, collauth.Config{Type: b.AuthType, Fields: b.AuthFields}, req)
	if err != nil {
		x.res.Outcome = OutcomeError
		x.res.Error = x.p.Registry.Mask("auth: " + err.Error())
		return false
	}
	b.Headers = mergeAuthHeaders(b.Headers, req.Header)
	if req.URL != b.URL {
		if pu, err := url.Parse(req.URL); err == nil && pu.Host != "" {
			b.URL = req.URL
		}
	}
	if res.Applied != "" {
		b.AuthApplied = true
		b.Applied = append(b.Applied, res.Applied)
	}
	x.res.Applied = append(x.res.Applied, b.Applied...)
	x.res.Warnings = append(x.res.Warnings, res.Warnings...)
	b.Applied = nil
	return true
}

// mergeAuthHeaders keeps the request's header order and replaces or appends
// only what the authenticator changed.
func mergeAuthHeaders(orig []varstore.KV, got http.Header) []varstore.KV {
	have := http.Header{}
	for _, h := range orig {
		have.Add(h.Key, h.Value)
	}
	out := orig
	for k, vs := range got {
		if sameValues(have[k], vs) {
			continue
		}
		kept := out[:0:0]
		for _, h := range out {
			if !strings.EqualFold(h.Key, k) {
				kept = append(kept, h)
			}
		}
		out = kept
		for _, v := range vs {
			out = append(out, varstore.KV{Key: k, Value: v})
		}
	}
	return out
}

func sameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
