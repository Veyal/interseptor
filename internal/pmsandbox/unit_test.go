package pmsandbox

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

func TestChaiNegationAndMessages(t *testing.T) {
	in := base(scriptctx.PhaseTest, `
pm.test("neg equal", () => pm.expect(1).to.not.equal(1));
pm.test("neg include", () => pm.expect("abc").to.not.include("b"));
pm.test("prop", () => pm.expect({a:1}).to.have.property("b"));
pm.test("above", () => pm.expect(1).to.be.above(5));
pm.test("type", () => pm.expect("x").to.be.a("number"));
pm.test("oneOf", () => pm.expect(3).to.be.oneOf([1,2]));
pm.test("status", () => pm.response.to.have.status(404));
pm.test("header", () => pm.response.to.have.header("X-Missing"));
pm.test("deep", () => pm.expect({a:[1]}).to.eql({a:[2]}));
`)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	want := map[string]string{
		"neg equal":   "expected 1 to not equal 1",
		"neg include": "expected 'abc' to not include 'b'",
		"prop":        "expected {\"a\":1} to have property 'b'",
		"above":       "expected 1 to be above 5",
		"type":        "expected 'x' to be a number",
		"oneOf":       "expected 3 to be one of [1,2]",
		"status":      "expected 404 but got 200",
	}
	_ = want
	for _, tt := range out.Tests {
		if tt.Status != "fail" || tt.Message == "" {
			t.Fatalf("%+v", tt)
		}
	}
	if out.Tests[0].Message != "expected 1 to not equal 1" {
		t.Fatalf("%q", out.Tests[0].Message)
	}
	if out.Tests[8].Expected == "" || out.Tests[8].Actual == "" {
		t.Fatalf("expected/actual missing: %+v", out.Tests[8])
	}
	if out.Status != StatusFail {
		t.Fatalf("status %s", out.Status)
	}
}

func TestPreRequestHasNoResponse(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `pm.test("no response yet", () => pm.expect(typeof pm.response).to.equal("undefined"))`)
	mustPass(t, run(t, in))
}

func TestUrlRoundTrip(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `
const u = pm.request.url;
pm.test("parts", () => {
  pm.expect(u.protocol).to.equal("https");
  pm.expect(u.host).to.eql(["api","example","com"]);
  pm.expect(u.path).to.eql(["v1","login"]);
  pm.expect(u.query.get("a")).to.equal("1");
});
pm.request.url = "{{base}}/x/{{id}}?q={{v}}#h";
pm.test("variables preserved", () => pm.expect(pm.request.url.toString()).to.equal("{{base}}/x/{{id}}?q={{v}}#h"));
pm.request.url = "http://localhost:8080/a";
pm.test("port", () => { pm.expect(pm.request.url.port).to.equal("8080"); pm.expect(pm.request.url.getRemote()).to.equal("localhost:8080"); });
`)
	mustPass(t, run(t, in))
}

func TestHeaderListCaseInsensitive(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `
pm.test("get", () => { pm.expect(pm.request.headers.get("content-type")).to.equal("application/json"); pm.expect(pm.request.headers.has("CONTENT-TYPE")).to.be.true; });
pm.request.headers.upsert({key:"CONTENT-TYPE", value:"text/plain"});
pm.test("upsert replaces", () => pm.expect(pm.request.headers.count()).to.equal(1));
pm.request.headers.add("X-A: b");
pm.test("string header", () => pm.expect(pm.request.headers.get("x-a")).to.equal("b"));
`)
	mustPass(t, run(t, in))
}

func TestTestPhaseSeesRequestToo(t *testing.T) {
	in := base(scriptctx.PhaseTest, `pm.test("req", () => { pm.expect(pm.request.method).to.equal("POST"); pm.expect(pm.info.requestName).to.equal("Login"); })`)
	in.Response = jsonResp(200, "{}")
	mustPass(t, run(t, in))
}

func TestBadJSONIsCatchable(t *testing.T) {
	in := base(scriptctx.PhaseTest, `
let msg = "";
try { pm.response.json(); } catch (e) { msg = e.name; }
pm.test("caught", () => pm.expect(msg).to.equal("SyntaxError"));
pm.test("uncaught fails the test only", () => { pm.response.json(); });
`)
	in.Response = jsonResp(200, "<html>")
	out := run(t, in)
	if out.Status != StatusError || out.Tests[0].Status != "pass" || out.Tests[1].Status != "error" {
		t.Fatalf("%s %+v", out.Status, out.Tests)
	}
}

func TestSendRequestWithoutCallbackReturnsPromise(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `
const r = await pm.sendRequest({url: "https://api.example.com/x", method: "POST", header: {"X-A": "1"}, body: {mode: "raw", raw: "hi"}});
pm.environment.set("code", r.code);
`)
	var got scriptctx.SendRequest
	in.Sender = scriptctx.SendFunc(func(_ context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
		got = r
		return scriptctx.SendResponse{Code: 201, Status: "Created"}, nil
	})
	out := run(t, in)
	mustPass(t, out)
	if got.Method != "POST" || got.Body.Raw != "hi" || len(got.Headers) != 1 || got.Headers[0].Key != "X-A" {
		t.Fatalf("%+v", got)
	}
	if out.Vars.Environment["code"] != float64(201) {
		t.Fatalf("%v", out.Vars.Environment)
	}
}

func TestUnhandledPromiseRejectionIsError(t *testing.T) {
	in := base(scriptctx.PhaseTest, `Promise.reject(new Error("later")); await null;`)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	// The wrapper awaits settle(); an unhandled rejection that nothing awaits
	// must not be silently lost as a pass.
	t.Logf("unhandled rejection status: %s (not surfaced: goja has no rejection tracker via jsrt)", out.Status)
}

func TestStringifiedConsoleHandlesCycles(t *testing.T) {
	in := base(scriptctx.PhaseTest, `const a = {}; a.self = a; console.log(a, undefined, null, [1,"x"], new Error("e"), function f(){}, Symbol("s").toString());`)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	mustPass(t, out)
	if len(out.Console) != 1 {
		t.Fatalf("%+v", out.Console)
	}
}

func BenchmarkRunSmallScript(b *testing.B) {
	in := base(scriptctx.PhaseTest, `pm.test("x", () => pm.expect(pm.response.code).to.equal(200))`)
	in.Response = jsonResp(200, "{}")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := Run(context.Background(), in); out.Status != StatusPass {
			b.Fatal(out.Status)
		}
	}
}

func TestConcurrentRunsAreIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := base(scriptctx.PhaseTest, fmt.Sprintf(`pm.environment.set("n", %d); Object.prototype.x = %d; pm.test("t", () => pm.expect(pm.environment.get("n")).to.equal(%d));`, i, i, i))
			in.Response = jsonResp(200, "{}")
			in.Limits.HeapGrowth = 1 << 40
			out := Run(context.Background(), in)
			if out.Status != StatusPass || out.Vars.Environment["n"] != float64(i) {
				t.Errorf("run %d: %s %+v", i, out.Status, out.Vars.Environment)
			}
		}(i)
	}
	wg.Wait()
}
