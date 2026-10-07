package collexec

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// scripted is a fake engine: the source text selects a behaviour.
func scripted(order *[]string) Executor {
	return ExecutorFunc(func(_ context.Context, c ScriptCall) (ScriptOutcome, error) {
		if order != nil {
			*order = append(*order, c.Owner+":"+c.Phase)
		}
		switch strings.TrimSpace(c.Source) {
		case "set-header":
			c.Ctx.Request.Headers = append(c.Ctx.Request.Headers, varstore.KV{Key: "X-From-Script", Value: "{{minted}}"})
			_ = c.Ctx.Vars.Set(VarCollection, "minted", "m-1")
		case "set-secret-var":
			_ = c.Ctx.Vars.Set(VarEnvironment, "token", canary+"-rotated")
		case "pass-test":
			c.Ctx.AddTest(TestResult{Name: "status is 200", Status: TestPass})
			if c.Ctx.Response.Code != 200 {
				c.Ctx.AddTest(TestResult{Name: "bad", Status: TestFail})
			}
		case "fail-test":
			c.Ctx.AddTest(TestResult{Name: "boom", Status: TestFail, Actual: "got " + canary})
		case "next":
			c.Ctx.SetNextRequest("Login")
		case "skip":
			c.Ctx.SkipRequest()
		case "console":
			return ScriptOutcome{Console: []jsrt.ConsoleEntry{{Level: "log", Text: "token=" + canary}}}, nil
		case "unsupported":
			return ScriptOutcome{}, &UnsupportedError{API: "pm.visualizer"}
		case "throw":
			return ScriptOutcome{}, errors.New("ReferenceError: x is not defined")
		}
		return ScriptOutcome{}, nil
	})
}

func scriptChain(url string, coll, folder, req string, listen string) Chain {
	ch := chainOf(store.Item{Name: "req", Method: "GET", URL: js(url)})
	ch.Collection.Events = events(listen, coll)
	if coll == "" {
		ch.Collection.Events = nil
	}
	if folder != "" {
		ch.Folders = []store.Item{{UID: "f1", Kind: "folder", Name: "folder", Events: events(listen, folder)}}
	}
	if req != "" {
		ch.Item.Events = events(listen, req)
	}
	return ch
}

func trustAll(t *testing.T, e *env, ch Chain) {
	t.Helper()
	for _, listen := range []string{ListenPre, ListenTest} {
		for _, s := range ch.scripts(listen) {
			if err := e.st.TrustScript(ch.Collection.UID, s.Hash); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPreScriptsRunOuterToInnerAndMutateTheRequest(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	var order []string
	e.pipe.Exec = scripted(&order)
	ch := scriptChain(rec.srv.URL, "set-header", "console", "", ListenPre)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeSent {
		t.Fatalf("%+v", res)
	}
	if strings.Join(order, ",") != "collection:prerequest,folder:prerequest" {
		t.Fatalf("order %v", order)
	}
	req, _ := rec.last()
	if req.Header.Get("X-From-Script") != "m-1" {
		t.Fatalf("script header/var not applied: %v", req.Header)
	}
	if len(res.VarChanges) != 1 || res.VarChanges[0].Scope != VarCollection || res.VarChanges[0].Key != "minted" {
		t.Fatalf("var changes %+v", res.VarChanges)
	}
}

func TestUntrustedScriptsNeverRunButRequestStillGoes(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	called := false
	e.pipe.Exec = ExecutorFunc(func(context.Context, ScriptCall) (ScriptOutcome, error) { called = true; return ScriptOutcome{}, nil })
	ch := scriptChain(rec.srv.URL, "set-header", "", "", ListenPre)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if called || res.Outcome != OutcomeSent || len(res.Scripts) != 1 || !strings.Contains(res.Scripts[0].Reason, "quarantined") {
		t.Fatalf("called=%v %+v", called, res)
	}
}

func TestQuarantineCanBlockHeadlessRuns(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "set-header", "", "", ListenPre)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off", FailOnQuarantine: true})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockQuarantined || rec.count() != 0 {
		t.Fatalf("%+v", res)
	}
	// --no-scripts is not quarantine: the request goes, scripts are skipped.
	res = e.step(StepInput{Chain: ch, ScopePolicy: "off", FailOnQuarantine: true, NoScripts: true})
	if res.Outcome != OutcomeSent {
		t.Fatalf("no-scripts: %+v", res)
	}
}

func TestEditingAScriptInvalidatesItsTrust(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "set-header", "", "", ListenPre)
	trustAll(t, e, ch)
	ch.Collection.Events = events(ListenPre, "set-header // edited")
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if len(res.Scripts) != 1 || !strings.Contains(res.Scripts[0].Reason, "quarantined") {
		t.Fatalf("edit kept trust: %+v", res.Scripts)
	}
}

func TestTrustHashMatchesCollectionPackage(t *testing.T) {
	ch := scriptChain("http://x", "set-header", "", "", ListenPre)
	got := ch.scripts(ListenPre)
	want := collection.EventSources(ch.Collection.Events)
	if len(got) != 1 || len(want) != 1 || got[0].Hash != collection.ScriptHash(want[0], nil, nil) {
		t.Fatalf("hash mismatch with internal/collection: %v %v", got, want)
	}
}

func TestNoExecutorReportsScriptsAsSkippedNotPassed(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	ch := scriptChain(rec.srv.URL, "", "", "pass-test", ListenTest)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeSent || len(res.Tests) != 0 || len(res.Scripts) != 1 || !strings.Contains(res.Scripts[0].Reason, "no script engine") {
		t.Fatalf("%+v", res)
	}
}

func TestPostScriptsSeeResponseAndRecordTests(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "pass-test", "", "fail-test", ListenTest)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off", Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"token": canary}, "token")}})
	if len(res.Tests) != 2 || res.Tests[0].Status != TestPass || res.Tests[0].Owner != "collection" || res.Tests[1].Status != TestFail || res.Tests[1].Owner != "request" {
		t.Fatalf("tests %+v", res.Tests)
	}
	if !res.Failed() {
		t.Fatal("Failed() should be true")
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("secret leaked via test output: %s", raw)
	}
}

func TestPostScriptFailureStatusesDoNotLookLikeFailures(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "unsupported", "throw", "", ListenTest)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeSent || len(res.Tests) != 2 || res.Tests[0].Status != TestUnsupported || res.Tests[1].Status != TestError {
		t.Fatalf("%+v", res.Tests)
	}
}

func TestPreScriptErrorStopsTheSend(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "throw", "", "", ListenPre)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeError || rec.count() != 0 || !strings.Contains(res.Error, "pre-request script failed") {
		t.Fatalf("%+v", res)
	}
}

func TestSkipAndSetNextRequest(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "skip", "", "", ListenPre)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if res.Outcome != OutcomeSkipped || rec.count() != 0 {
		t.Fatalf("%+v", res)
	}
	ch = scriptChain(rec.srv.URL, "", "", "next", ListenTest)
	trustAll(t, e, ch)
	res = e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	if !res.Flow.HasNext || res.Flow.NextRequest != "Login" {
		t.Fatalf("flow %+v", res.Flow)
	}
}

func TestConsoleAndVarChangesAreMasked(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = scripted(nil)
	ch := scriptChain(rec.srv.URL, "console", "set-secret-var", "", ListenPre)
	trustAll(t, e, ch)
	res := e.step(StepInput{Chain: ch, ScopePolicy: "off",
		Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"token": canary}, "token")}})
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("secret leaked: %s", raw)
	}
	if len(res.Console) != 1 || strings.Contains(res.Console[0].Text, canary) {
		t.Fatalf("console %+v", res.Console)
	}
	// The committed value is still available to the caller (not in JSON).
	if len(res.VarChanges) != 1 || res.VarChanges[0].Value != canary+"-rotated" || !res.VarChanges[0].Secret || res.VarChanges[0].Display != "[secret]" {
		t.Fatalf("changes %+v", res.VarChanges)
	}
}

func TestScriptVarWritesFeedLaterResolution(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	e.pipe.Exec = ExecutorFunc(func(_ context.Context, c ScriptCall) (ScriptOutcome, error) {
		_ = c.Ctx.Vars.Set(VarLocal, "path", "dyn")
		c.Ctx.Request.URL += "/{{path}}"
		return ScriptOutcome{}, nil
	})
	ch := scriptChain(rec.srv.URL, "x", "", "", ListenPre)
	trustAll(t, e, ch)
	e.step(StepInput{Chain: ch, ScopePolicy: "off"})
	req, _ := rec.last()
	if req.URL.Path != "/dyn" {
		t.Fatalf("path %q", req.URL.Path)
	}
}

func TestVarsScopesAndUnset(t *testing.T) {
	v := newVars([]varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"a": "1"}), layer(varstore.ScopeData, map[string]string{"d": "x"})}, nil, nil)
	if got, _ := v.Get("a"); got != "1" {
		t.Fatal("get")
	}
	if err := v.Set(VarData, "d", "y"); err == nil {
		t.Fatal("data scope must be read-only")
	}
	if err := v.Set("bogus", "k", "v"); err == nil {
		t.Fatal("unknown scope accepted")
	}
	_ = v.Set(VarGlobals, "g", "G")
	_ = v.Set(VarEnvironment, "a", "2") // environment beats global but env layer exists: updated in place
	if got, _ := v.Get("a"); got != "2" {
		t.Fatal("update")
	}
	_ = v.Unset(VarEnvironment, "a")
	if _, ok := v.Get("a"); ok {
		t.Fatal("unset")
	}
	if v.ToObject(VarGlobals)["g"] != "G" {
		t.Fatal("toObject")
	}
	if n := len(v.Changes()); n != 2 {
		t.Fatalf("collapsed changes: %+v", v.Changes())
	}
}

func TestSetCookieFeedsTheJarForTheNextStep(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
		}
		w.Write([]byte("ok"))
	})
	login := store.Item{UID: "a", Method: "POST", URL: js(rec.srv.URL + "/login")}
	e.step(StepInput{Chain: chainOf(login), ScopePolicy: "off", EnvUID: "env1", Identity: "alice"})
	next := store.Item{UID: "b", Method: "GET", URL: js(rec.srv.URL + "/me")}
	e.step(StepInput{Chain: chainOf(next), ScopePolicy: "off", EnvUID: "env1", Identity: "alice"})
	if req, _ := rec.last(); req.Header.Get("Cookie") != "sid=abc" {
		t.Fatalf("cookie not attached: %v", req.Header)
	}
	// A different identity partition has its own jar.
	e.step(StepInput{Chain: chainOf(next), ScopePolicy: "off", EnvUID: "env1", Identity: "bob"})
	if req, _ := rec.last(); req.Header.Get("Cookie") != "" {
		t.Fatalf("cookie leaked across identities: %v", req.Header)
	}
	// An explicit Cookie header wins over the jar.
	next.Headers = js([]m{{"key": "Cookie", "value": "manual=1"}})
	e.step(StepInput{Chain: chainOf(next), ScopePolicy: "off", EnvUID: "env1", Identity: "alice"})
	if req, _ := rec.last(); req.Header.Get("Cookie") != "manual=1" {
		t.Fatalf("explicit cookie overridden: %v", req.Header)
	}
}
