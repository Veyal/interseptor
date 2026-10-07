package scriptworker

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

func input(script string) pmsandbox.Input {
	return pmsandbox.Input{
		Phase: scriptctx.PhaseTest, Script: script, Caps: scriptctx.DefaultCaps(),
		Request:  &scriptctx.Request{Method: "GET", URL: "https://api.example.com/v1"},
		Response: &scriptctx.Response{Code: 200, Status: "OK", Body: []byte(`{"token":"abc"}`), ResponseTime: 12 * time.Millisecond},
	}
}

func errKind(o pmsandbox.Output) string {
	if len(o.Errors) == 0 {
		return ""
	}
	return o.Errors[0].Kind
}

func TestSubprocessRunsScriptAndReturnsState(t *testing.T) {
	in := input(`pm.test("code", function(){ pm.expect(pm.response.code).to.equal(200) });
pm.environment.set("tok", pm.response.json().token); console.log("hi")`)
	in.Vars = scriptctx.Vars{Environment: map[string]any{"a": "1"}}
	o := testWorker("worker").Run(context.Background(), in)
	if o.Status != pmsandbox.StatusPass || len(o.Tests) != 1 {
		t.Fatalf("status %s errors %+v tests %+v", o.Status, o.Errors, o.Tests)
	}
	if o.Vars.Environment["tok"] != "abc" || o.Vars.Environment["a"] != "1" {
		t.Fatalf("vars %+v", o.Vars)
	}
	if len(o.Console) != 1 || o.Console[0].Text != "hi" {
		t.Fatalf("console %+v", o.Console)
	}
}

func TestSubprocessDoesNotInheritParentEnvironment(t *testing.T) {
	t.Setenv("SW_CANARY_SECRET", "canary-env-value")
	o := testWorker("worker").Run(context.Background(), input(`console.log(typeof process, typeof require("crypto"))`))
	for _, c := range o.Console {
		if strings.Contains(c.Text, "canary-env-value") {
			t.Fatal("env leaked")
		}
	}
}

func TestInfiniteLoopKilledParentSurvives(t *testing.T) {
	in := input(`while(true){}`)
	in.Limits.Timeout = 300 * time.Millisecond
	begin := time.Now()
	o := testWorker("worker").Run(context.Background(), in)
	if o.Status != pmsandbox.StatusError || len(o.Errors) == 0 {
		t.Fatalf("status %s errors %+v", o.Status, o.Errors)
	}
	if time.Since(begin) > 6*time.Second {
		t.Fatalf("took %v", time.Since(begin))
	}
	// the supervisor is still usable afterwards
	if o2 := testWorker("worker").Run(context.Background(), input(`pm.test("a",function(){})`)); o2.Status != pmsandbox.StatusPass {
		t.Fatalf("follow-up run failed: %+v", o2.Errors)
	}
}

func TestPromiseLoopKilled(t *testing.T) {
	in := input(`(function f(){ Promise.resolve().then(f) })()`)
	in.Limits.Timeout = 300 * time.Millisecond
	o := testWorker("worker").Run(context.Background(), in)
	if o.Status != pmsandbox.StatusError {
		t.Fatalf("status %s", o.Status)
	}
}

func TestMemoryBombsKilledParentSurvives(t *testing.T) {
	bombs := map[string]string{
		"array-of-arrays": `var a=[]; while(true){ a.push(new Array(1000000).fill(1)); }`,
		"string-concat":   `var s="x".repeat(1<<20), a=[]; while(true){ a.push(s+Math.random()); }`,
		"object-growth":   `var o={}; for(var i=0;;i++){ o["k"+i]=new Array(1000).fill(i); }`,
		"typed-arrays":    `var a=[]; while(true){ a.push(new Uint8Array(8<<20)); }`,
	}
	for name, src := range bombs {
		t.Run(name, func(t *testing.T) {
			in := input(src)
			in.Limits.Timeout = 20 * time.Second
			begin := time.Now()
			o := testWorker("worker").Run(context.Background(), in)
			if o.Status != pmsandbox.StatusError || len(o.Errors) == 0 {
				t.Fatalf("status %s errors %+v", o.Status, o.Errors)
			}
			if k := errKind(o); k != KindMemory && k != "limit" {
				t.Fatalf("kind %q (%s)", k, o.Errors[0].Message)
			}
			if d := time.Since(begin); d > 15*time.Second {
				t.Fatalf("took %v", d)
			}
		})
	}
}

func TestParentRSSWatchdogKillsRogueWorker(t *testing.T) {
	w := testWorker("hog")
	w.MaxRSS = 100 << 20
	in := input(`1`)
	in.Limits.Timeout = 20 * time.Second
	begin := time.Now()
	o := w.Run(context.Background(), in)
	if errKind(o) != KindMemory {
		t.Fatalf("errors %+v", o.Errors)
	}
	if time.Since(begin) > 10*time.Second {
		t.Fatalf("took %v", time.Since(begin))
	}
}

func TestHungWorkerKilledAtDeadline(t *testing.T) {
	w := Subprocess{Path: "/bin/sleep", Args: []string{"30"}, Grace: 200 * time.Millisecond}
	in := input(`1`)
	in.Limits.Timeout = 200 * time.Millisecond
	begin := time.Now()
	o := w.Run(context.Background(), in)
	if errKind(o) != KindTimeout || time.Since(begin) > 5*time.Second {
		t.Fatalf("errors %+v after %v", o.Errors, time.Since(begin))
	}
}

func TestCancelKillsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	in := input(`while(true){}`)
	in.Limits.Timeout = 30 * time.Second
	begin := time.Now()
	o := testWorker("worker").Run(ctx, in)
	if errKind(o) != "canceled" || time.Since(begin) > 5*time.Second {
		t.Fatalf("errors %+v after %v", o.Errors, time.Since(begin))
	}
}

func TestCrashAndGarbageWorkers(t *testing.T) {
	for name, w := range map[string]Subprocess{
		"crash":    {Path: "/usr/bin/false"},
		"garbage":  {Path: "/bin/sh", Args: []string{"-c", "printf 'GARBAGEGARBAGE'; sleep 1"}},
		"missing":  {Path: "/nonexistent/worker"},
		"huge-len": {Path: "/bin/sh", Args: []string{"-c", `printf '\377\377\377\377R'; sleep 1`}},
	} {
		o := w.Run(context.Background(), input(`1`))
		if o.Status != pmsandbox.StatusError || len(o.Errors) != 1 {
			t.Errorf("%s: %s %+v", name, o.Status, o.Errors)
		}
	}
}

func TestSendProxiedThroughParentWithScopeGuard(t *testing.T) {
	var sent atomic.Int32
	var scoped []string
	in := input(`
pm.sendRequest("https://api.example.com/ok", function(e, r){ pm.environment.set("code", String(r.code)); });
pm.sendRequest("https://evil.example.org/x", function(e, r){ pm.environment.set("blocked", e ? "yes" : "no"); });`)
	in.Sender = scriptctx.SendFunc(func(_ context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
		sent.Add(1)
		return scriptctx.SendResponse{Code: 201, Status: "Created", Body: []byte("ok"), ResponseTime: time.Millisecond}, nil
	})
	in.ScopeCheck = func(u string) error {
		scoped = append(scoped, u)
		if strings.Contains(u, "evil.example.org") {
			return errors.New("out of scope")
		}
		return nil
	}
	o := testWorker("worker").Run(context.Background(), in)
	if o.Status == pmsandbox.StatusError {
		t.Fatalf("errors %+v", o.Errors)
	}
	if sent.Load() != 1 {
		t.Fatalf("sender calls = %d, want 1 (out-of-scope must not reach it)", sent.Load())
	}
	if o.Vars.Environment["code"] != "201" || o.Vars.Environment["blocked"] != "yes" {
		t.Fatalf("vars %+v sends %+v", o.Vars.Environment, o.Sends)
	}
}

func TestParentEnforcesSendBudgetIndependently(t *testing.T) {
	s := &session{in: pmsandbox.Input{Limits: pmsandbox.Limits{MaxSends: 2}, Sender: scriptctx.SendFunc(
		func(context.Context, scriptctx.SendRequest) (scriptctx.SendResponse, error) {
			return scriptctx.SendResponse{Code: 200}, nil
		})},
		ctx: context.Background()}
	var errs int
	got := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		r := serveOnce(s)
		got = append(got, r.Err)
		if r.Err != "" {
			errs++
		}
	}
	if errs != 2 {
		t.Fatalf("replies %v", got)
	}
}

func TestScrubAppliedToWorkerOutput(t *testing.T) {
	in := input(`console.log("token=canary-secret-123"); pm.test("t canary-secret-123", function(){})`)
	in.Scrub = func(s string) string { return strings.ReplaceAll(s, "canary-secret-123", "[REDACTED]") }
	o := testWorker("worker").Run(context.Background(), in)
	for _, c := range o.Console {
		if strings.Contains(c.Text, "canary-secret-123") {
			t.Fatal("console leaked")
		}
	}
	for _, tr := range o.Tests {
		if strings.Contains(tr.Name, "canary-secret-123") {
			t.Fatal("test name leaked")
		}
	}
}

func TestClockAndSeedPinned(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := input(`pm.environment.set("t", String(Date.now())); pm.environment.set("r", String(Math.random()))`)
	in.Clock = func() time.Time { return now }
	in.Rand = func() float64 { return 0.5 }
	a := testWorker("worker").Run(context.Background(), in)
	b := testWorker("worker").Run(context.Background(), in)
	if a.Vars.Environment["t"] != "1767323045000" || a.Vars.Environment["t"] != b.Vars.Environment["t"] || a.Vars.Environment["r"] != b.Vars.Environment["r"] {
		t.Fatalf("a=%v b=%v errs=%+v", a.Vars.Environment, b.Vars.Environment, a.Errors)
	}
}

type countingExec struct{ n int }

func (c *countingExec) Run(context.Context, pmsandbox.Input) pmsandbox.Output {
	c.n++
	return pmsandbox.Output{}
}

func TestRouterPolicy(t *testing.T) {
	w, l := &countingExec{}, &countingExec{}
	r := Router{Worker: w, Local: l}
	r.Run(context.Background(), true, pmsandbox.Input{})
	r.Run(context.Background(), false, pmsandbox.Input{})
	if w.n != 2 || l.n != 0 {
		t.Fatalf("default policy must send everything to the worker: w=%d l=%d", w.n, l.n)
	}
	r.InProcessTrusted = true
	r.Run(context.Background(), true, pmsandbox.Input{})
	r.Run(context.Background(), false, pmsandbox.Input{})
	if w.n != 3 || l.n != 1 {
		t.Fatalf("trusted-in-process policy: w=%d l=%d", w.n, l.n)
	}
	if _, ok := (Router{InProcessTrusted: true}).For(true).(InProcess); !ok {
		t.Fatal("default local executor should be InProcess")
	}
}

var _ Executor = Subprocess{}
var _ Executor = InProcess{}

// serveOnce drives session.serve synchronously and captures its reply.
func serveOnce(s *session) replyWire {
	var captured replyWire
	var buf bytesBuf
	s.stdin = &buf
	s.serve(FrameSend, callWire{ID: 7, Send: &scriptctx.SendRequest{Method: "GET", URL: "https://api.example.com/"}})
	_ = captured
	typ, p, err := ReadFrame(bufioReader(&buf))
	if err != nil || typ != FrameReply {
		panic("no reply")
	}
	_ = jsonUnmarshal(p, &captured)
	return captured
}
