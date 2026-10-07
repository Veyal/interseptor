package collexec

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

const canary = "CANARY-secret-token-7f3a9c"

func TestSingleSendResolvesAuthQueryAndCaptures(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	ch := chainOf(store.Item{
		Name: "get user", Method: "GET",
		URL:     js(m{"raw": "{{baseUrl}}/users/:id", "variable": []m{{"key": "id", "value": "42"}}}),
		Params:  js([]m{{"key": "q", "value": "a b"}, {"key": "off", "value": "x", "disabled": true}}),
		Headers: js([]m{{"key": "X-Trace", "value": "t-{{trace}}"}}),
	})
	ch.Collection.Auth = js(m{"type": "bearer", "bearer": []m{{"key": "token", "value": "{{token}}"}}})
	res := e.step(StepInput{
		Chain: ch, Source: SourceRunner, RunID: "run1", Iteration: 2, EnvUID: "env1", Identity: "alice",
		Layers: []varstore.Layer{
			layer(varstore.ScopeEnvironment, map[string]string{"baseUrl": rec.srv.URL, "token": canary, "trace": "9"}, "token"),
		},
	})
	if res.Outcome != OutcomeSent || res.Response == nil || res.Response.Status != 200 {
		t.Fatalf("result: %+v", res)
	}
	req, _ := rec.last()
	if req.URL.Path != "/users/42" || req.URL.RawQuery != "q=a%20b" {
		t.Fatalf("url: %s", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Bearer "+canary || req.Header.Get("X-Trace") != "t-9" {
		t.Fatalf("headers: %v", req.Header)
	}
	// Captured as a collection flow with flow context.
	f, err := e.st.GetFlow(res.FlowID)
	if err != nil || f.Flags&store.FlagCollection == 0 {
		t.Fatalf("flow flags: %v %+v", err, f)
	}
	ctx, ok, _ := e.st.GetFlowCtx(res.FlowID)
	if !ok || ctx.RunID != "run1" || ctx.ItemUID != "item1" || ctx.Iteration != 2 || ctx.EnvUID != "env1" || ctx.Identity != "alice" || ctx.TemplateHash == "" {
		t.Fatalf("flow ctx: %+v ok=%v", ctx, ok)
	}
	// The wire flow keeps the true bytes (secret included) but the result never does.
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("secret leaked into result: %s", raw)
	}
	if !contains(res.Applied, "auth:bearer") {
		t.Fatalf("applied: %v", res.Applied)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAIRequestsCarryAIFlag(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL)}), AI: true, Source: SourceMCP,
		ScopePolicy: store.ScopePolicyOff})
	f, _ := e.st.GetFlow(res.FlowID)
	if f.Flags&store.FlagAI == 0 || f.Flags&store.FlagCollection == 0 {
		t.Fatalf("flags %b", f.Flags)
	}
}

func TestUnresolvedVariablesBlockTheSend(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL + "/{{nope}}")}), ScopePolicy: "off"})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockUnresolved || len(res.Unresolved) != 1 || res.Unresolved[0] != "nope" {
		t.Fatalf("%+v", res)
	}
	if rec.count() != 0 {
		t.Fatal("blocked request reached the server")
	}
}

func TestUnresolvedLiteralOptIn(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL + "/{{nope}}"),
		Settings: js(m{"unresolved": "literal"})}), ScopePolicy: "off"})
	if res.Outcome != OutcomeSent {
		t.Fatalf("%+v", res)
	}
	req, _ := rec.last()
	if !strings.Contains(req.URL.Path, "nope") {
		t.Fatalf("path %q", req.URL.Path)
	}
}

func TestScopeBlockWarnOff(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("other.example.com")
	it := store.Item{Method: "GET", URL: js(rec.srv.URL + "/x")}

	// Non-interactive sources block by default.
	for _, src := range []Source{SourceRunner, SourceCLI, SourceMCP, SourceScript, ""} {
		res := e.step(StepInput{Chain: chainOf(it), Source: src})
		if res.Outcome != OutcomeBlocked || res.BlockReason != BlockScope {
			t.Fatalf("%s: %+v", src, res)
		}
	}
	if rec.count() != 0 {
		t.Fatal("blocked send reached the server")
	}
	// Interactive sends warn and go through.
	res := e.step(StepInput{Chain: chainOf(it), Source: SourceUI})
	if res.Outcome != OutcomeSent || len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "out of scope") {
		t.Fatalf("warn: %+v", res)
	}
	if rec.count() != 1 {
		t.Fatal("warn send did not reach the server")
	}
	// Explicit override to off.
	res = e.step(StepInput{Chain: chainOf(it), Source: SourceRunner, ScopePolicy: store.ScopePolicyOff})
	if res.Outcome != OutcomeSent || len(res.Warnings) != 0 {
		t.Fatalf("off: %+v", res)
	}
	// Collection-level override applies to the runner.
	ch := chainOf(it)
	ch.Collection.ScopePolicy = store.ScopePolicyWarn
	res = e.step(StepInput{Chain: ch, Source: SourceRunner})
	if res.Outcome != OutcomeSent || len(res.Warnings) == 0 {
		t.Fatalf("collection warn: %+v", res)
	}
}

func TestScopeInScopeHostSendsSilently(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL)}), Source: SourceRunner})
	if res.Outcome != OutcomeSent || len(res.Warnings) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestPrivateDestinationNeedsExplicitScopeUnderBlock(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	// Everything "in scope" but not an explicit allow-list: loopback must be refused at dial time.
	e.pipe.Scope = openScope{}
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL)}), Source: SourceRunner})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockScope {
		t.Fatalf("%+v", res)
	}
	if rec.count() != 0 {
		t.Fatal("guard let loopback through")
	}
}

type openScope struct{}

func (openScope) HostInScope(string) bool { return true }
func (openScope) HasIncludes() bool       { return false }

func TestOwnListenerRefusedEvenWithScopeOff(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	port := portOf(mustURL(t, rec.srv.URL))
	e.pipe.OwnPorts = []int{port}
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL)}), ScopePolicy: store.ScopePolicyOff, Source: SourceUI})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockOwn {
		t.Fatalf("%+v", res)
	}
	if rec.count() != 0 {
		t.Fatal("own listener was contacted")
	}
}

func TestBaseTargetPin(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	it := store.Item{Method: "GET", URL: js(rec.srv.URL)}
	res := e.step(StepInput{Chain: chainOf(it), EnvPin: "https://prod.example.com", Source: SourceRunner, ScopePolicy: store.ScopePolicyBlock})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockPin {
		t.Fatalf("%+v", res)
	}
	e.pipe.Scope = scopeOf1("127.0.0.1")
	res = e.step(StepInput{Chain: chainOf(it), EnvPin: "127.0.0.1:" + mustURL(t, rec.srv.URL).Port(), Source: SourceRunner})
	if res.Outcome != OutcomeSent {
		t.Fatalf("matching pin: %+v", res)
	}
}

func TestForwardingNeverBrokenByCaptureFailures(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	for name, w := range map[string]FlowCtxWriter{
		"error": failingCtx{}, "panic": panickingCtx{},
	} {
		e.pipe.Flows = w
		res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL)}), ScopePolicy: "off"})
		if res.Outcome != OutcomeSent || res.Response.Status != 200 {
			t.Fatalf("%s: capture failure broke the send: %+v", name, res)
		}
	}
	// A panicking executor in a post phase must not turn the send into an error either.
	e.pipe.Flows = e.st
	e.pipe.Exec = ExecutorFunc(func(ctxt context.Context, c ScriptCall) (ScriptOutcome, error) { panic("boom") })
	e.pipe.Trust = allTrusted{}
	ch := chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL), Events: events("test", "x")})
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeSent || len(res.Tests) != 1 || res.Tests[0].Status != TestError {
		t.Fatalf("%+v", res)
	}
}

type failingCtx struct{}

func (failingCtx) PutFlowCtx(store.FlowCtx) error { return http.ErrAbortHandler }

type panickingCtx struct{}

func (panickingCtx) PutFlowCtx(store.FlowCtx) error { panic("ctx writer exploded") }

func TestTransportErrorKeepsFlowAndReportsError(t *testing.T) {
	e := newEnv(t)
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js("http://127.0.0.1:1/")}), ScopePolicy: "off"})
	if res.Outcome != OutcomeError || res.FlowID == 0 || res.Response == nil || res.Response.Error == "" {
		t.Fatalf("%+v", res)
	}
}

func TestDuplicateHeadersKeepOrderOnTheWire(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL),
		Headers: js([]m{{"key": "X-Dup", "value": "one"}, {"key": "X-Dup", "value": "two"}, {"key": "x-lower", "value": "v"}})}), ScopePolicy: "off"})
	if res.Outcome != OutcomeSent {
		t.Fatalf("%+v", res)
	}
	req, _ := rec.last()
	if got := req.Header["X-Dup"]; len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("dup headers: %v", req.Header)
	}
}

func TestHostHeaderOverride(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(rec.srv.URL),
		Headers: js([]m{{"key": "Host", "value": "vhost.example.com"}})}), ScopePolicy: "off"})
	req, _ := rec.last()
	if req.Host != "vhost.example.com" {
		t.Fatalf("host %q", req.Host)
	}
}

func TestCollectionItemsSkipGlobalSessionHeadersByDefault(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.snd.SetSession(true, []sender.Header{{Key: "X-Session", Value: "s1"}})
	e.snd.SetSessionScope(func(string, string, int, string) bool { return true })
	it := store.Item{Method: "GET", URL: js(rec.srv.URL)}
	e.step(StepInput{Chain: chainOf(it), ScopePolicy: "off"})
	if req, _ := rec.last(); req.Header.Get("X-Session") != "" {
		t.Fatal("session header injected by default")
	}
	it.Settings = js(m{"useSession": true})
	e.step(StepInput{Chain: chainOf(it), ScopePolicy: "off"})
	if req, _ := rec.last(); req.Header.Get("X-Session") != "s1" {
		t.Fatal("useSession opt-in did not inject")
	}
}

func TestRedirectFollowRechecksScopePerHop(t *testing.T) {
	e := newEnv(t)
	var other *recorder
	other = newRecorder(t, nil)
	first := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", 302)
			return
		}
		if r.URL.Path == "/out" {
			http.Redirect(w, r, other.srv.URL+"/landed", 302)
			return
		}
		w.Write([]byte("b"))
	})
	e.pipe.Scope = scopeOf1("127.0.0.1")
	follow := js(m{"followRedirects": true})
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(first.srv.URL + "/a"), Settings: follow}), Source: SourceRunner})
	if res.Outcome != OutcomeSent || len(res.FlowIDs) != 2 || res.Response.Status != 200 {
		t.Fatalf("follow: %+v", res)
	}
	// Redirect to an out-of-scope host (localhost name differs from 127.0.0.1) stops the chase.
	e.pipe.Scope = scopeOf1("127.0.0.1")
	oURL := strings.Replace(other.srv.URL, "127.0.0.1", "localhost", 1)
	redirTo := newRecorder(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, oURL+"/landed", 302) })
	res = e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js(redirTo.srv.URL), Settings: follow}), Source: SourceRunner})
	if res.Outcome != OutcomeSent || res.Response.Status != 302 || other.count() != 0 {
		t.Fatalf("out-of-scope hop followed: %+v other=%d", res, other.count())
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "redirect not followed") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	_ = first
}
