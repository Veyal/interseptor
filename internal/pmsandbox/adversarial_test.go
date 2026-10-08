package pmsandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// adv runs a hostile script with tight limits and returns the output plus
// how long it took, failing the test if containment took too long.
func adv(t *testing.T, script string, mod func(*Input)) (Output, time.Duration) {
	t.Helper()
	in := base(scriptctx.PhaseTest, script)
	in.Response = jsonResp(200, "{}")
	in.Limits = Limits{Timeout: 300 * time.Millisecond, HeapGrowth: 96 << 20}
	if mod != nil {
		mod(&in)
	}
	start := time.Now()
	out := run(t, in)
	d := time.Since(start)
	if d > in.Limits.Timeout+5*time.Second {
		t.Fatalf("containment took %s", d)
	}
	return out, d
}

func wantKind(t *testing.T, out Output, kind string) {
	t.Helper()
	if out.Status != StatusError {
		t.Fatalf("status=%s want error; errors=%+v tests=%+v", out.Status, out.Errors, out.Tests)
	}
	for _, e := range out.Errors {
		if e.Kind == kind {
			return
		}
	}
	for _, tt := range out.Tests {
		if tt.Status == "error" && kind == "error" {
			return
		}
	}
	t.Fatalf("no %s error in %+v / %+v", kind, out.Errors, out.Tests)
}

func TestAdversarialLoops(t *testing.T) {
	cases := map[string]string{
		"sync loop":        `while(true){}`,
		"for loop":         `for(;;){ var x = 1 }`,
		"promise job loop": `function f(){ return Promise.resolve().then(f) } f(); await new Promise(()=>{});`,
		"async await loop": `while(true){ await null }`,
		"timer chain":      `function f(){ setTimeout(f,0) } f();`,
		"heavy compute":    `let n=0; for(let i=0;i<1e12;i++){ n+=i }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, _ := adv(t, src, nil)
			if out.Status != StatusError {
				t.Fatalf("status=%s %+v", out.Status, out.Errors)
			}
		})
	}
}

func TestAdversarialRegexBacktracking(t *testing.T) {
	// goja's regexp engine copes with this classic pattern; the test pins that
	// the run terminates inside the limits whatever the verdict.
	out, d := adv(t, `/(a+)+$/.test("a".repeat(50)+"!"); /^(([a-z])+.)+[A-Z]([a-z])+$/.test("a".repeat(40)+"!")`, nil)
	if d > 3*time.Second {
		t.Fatalf("slow: %s %+v", d, out.Errors)
	}
}

func TestAdversarialTimeoutKind(t *testing.T) {
	out, d := adv(t, `while(true){}`, nil)
	wantKind(t, out, "timeout")
	if d > 2*time.Second {
		t.Fatalf("slow interrupt %s", d)
	}
}

func TestAdversarialStackOverflow(t *testing.T) {
	out, _ := adv(t, `function r(n){ return r(n+1)+1 } r(0)`, nil)
	wantKind(t, out, "error")
}

func TestAdversarialMemoryBombs(t *testing.T) {
	cases := map[string]string{
		"repeat":        `"a".repeat(1e9)`,
		"repeat big n":  `"abc".repeat(2**31)`,
		"padStart":      `"a".padStart(1e9, "xy")`,
		"padEnd":        `"a".padEnd(1e9)`,
		"Array(n).join": `new Array(1e9).join("a")`,
		"Array(n)":      `new Array(1e10)`,
		"Array call":    `Array(1e10)`,
		"Buffer.alloc":  `Buffer.alloc(1e9)`,
		"ArrayBuffer":   `new ArrayBuffer(1e10)`,
		"Uint8Array":    `new Uint8Array(1e10)`,
		"Buffer.from":   `Buffer.from("a".repeat(1e8))`,
		"randomBytes":   `require("crypto").randomBytes(1e9)`,
		"randomWA":      `CryptoJS.lib.WordArray.random(1e9)`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, _ := adv(t, src, nil)
			wantKind(t, out, "error")
		})
	}
}

func TestAdversarialHeapGrowthBackstop(t *testing.T) {
	cases := map[string]string{
		"string doubling": `let s = "abcdefgh"; while (true) { s = s + s; }`,
		"array of arrays": `const keep = []; while (true) { keep.push(new Array(100000).fill(1)); }`,
		"object spam":     `const keep = []; while (true) { keep.push({a:"x".repeat(1000), b:[1,2,3]}); }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, _ := adv(t, src, func(in *Input) { in.Limits.Timeout = 10 * time.Second })
			if out.Status != StatusError {
				t.Fatalf("status=%s", out.Status)
			}
			kinds := ""
			for _, e := range out.Errors {
				kinds += e.Kind + " "
			}
			if !strings.Contains(kinds, "memory") && !strings.Contains(kinds, "error") && !strings.Contains(kinds, "timeout") {
				t.Fatalf("kinds=%s", kinds)
			}
		})
	}
}

func TestAdversarialPrototypePollution(t *testing.T) {
	out, _ := adv(t, `
Object.prototype.polluted = "yes";
Array.prototype.polluted = "yes";
String.prototype.polluted = "yes";
Object.prototype.toString = function(){ return "pwn" };
Function.prototype.call = function(){ return 1 };
pm.environment.set("__proto__", "x");
pm.environment.set("constructor", "y");
pm.test("inline", () => pm.expect({}.polluted).to.equal("yes"));
`, nil)
	if out.Status != StatusPass && out.Status != StatusFail && out.Status != StatusError {
		t.Fatalf("status=%s", out.Status)
	}
	// A second run in the same process must be pristine (fresh runtime).
	out2, _ := adv(t, `
pm.test("clean", () => {
  pm.expect(({}).polluted).to.be.undefined;
  pm.expect([].polluted).to.be.undefined;
  pm.expect("s".polluted).to.be.undefined;
  pm.expect(String({})).to.equal("[object Object]");
});
`, nil)
	mustPass(t, out2)
	// Hostile variable data must not leak into Object.prototype either.
	in := base(scriptctx.PhaseTest, `
pm.test("vars clean", () => { pm.expect({}.admin).to.be.undefined; pm.expect(Object.keys(pm.environment.toObject())).to.include("__proto__"); });
`)
	in.Response = jsonResp(200, "{}")
	if err := json.Unmarshal([]byte(`{"__proto__":{"admin":true},"constructor":"c"}`), &in.Vars.Environment); err != nil {
		t.Fatal(err)
	}
	mustPass(t, run(t, in))
}

func TestAdversarialEscapeProbes(t *testing.T) {
	out, _ := adv(t, `
const probes = {};
probes.h = typeof __h;
probes.in = typeof __in;
probes.fin = typeof globalThis.__fin;
probes.names = Object.getOwnPropertyNames(globalThis).filter(n => n.indexOf("__") === 0 && n !== "__isp_fin");
let viaFn; try { viaFn = Function("return typeof __h")(); } catch (e) { viaFn = "threw"; }
let viaEval; try { viaEval = eval("typeof __in"); } catch (e) { viaEval = "threw"; }
let viaCtor; try { viaCtor = (function(){}).constructor("return typeof __h")(); } catch (e) { viaCtor = "threw"; }
let viaThis; try { viaThis = this.constructor.constructor("return typeof __h")(); } catch (e) { viaThis = "threw"; }
pm.test("no host bindings", () => {
  pm.expect(probes.h).to.equal("undefined");
  pm.expect(probes.in).to.equal("undefined");
  pm.expect(probes.fin).to.equal("undefined");
  pm.expect(probes.names).to.eql([]);
  pm.expect(viaFn).to.equal("undefined");
  pm.expect(viaEval).to.equal("undefined");
  pm.expect(viaCtor).to.equal("undefined");
});
`, nil)
	mustPass(t, out)
}

func TestAdversarialForbiddenAPIs(t *testing.T) {
	cases := map[string]string{
		"require fs":            `require("fs")`,
		"require child_process": `require("child_process")`,
		"require net":           `require("net")`,
		"require http":          `require("http")`,
		"require os":            `require("os")`,
		"dynamic require":       `const n = ["f","s"].join(""); require(n)`,
		"process":               `process.env.HOME`,
		"fetch":                 `fetch("https://example.com")`,
		"XMLHttpRequest":        `new XMLHttpRequest()`,
		"WebSocket":             `new WebSocket("wss://example.com")`,
		"Function process":      `Function("return process")()`,
		"pm.require":            `pm.require("npm:lodash")`,
		"pm.vault":              `pm.vault.get("k")`,
		"import()":              `import("fs")`,
		"globalThis.process":    `globalThis.process.exit(1)`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, _ := adv(t, src, nil)
			if out.Status == StatusPass || out.Status == StatusFail {
				t.Fatalf("forbidden API ran: status=%s", out.Status)
			}
			if out.Status != StatusUnsupported && out.Status != StatusError {
				t.Fatalf("status=%s", out.Status)
			}
		})
	}
}

func TestAdversarialOutputFloods(t *testing.T) {
	out, _ := adv(t, `
for (let i = 0; i < 20000; i++) { console.log("x".repeat(1000) + i); }
`, func(in *Input) { in.Limits.Timeout = 30 * time.Second })
	total := 0
	for _, l := range out.Console {
		total += len(l.Text)
	}
	if total > 2<<20 {
		t.Fatalf("console not capped: %d bytes", total)
	}
	if len(out.Console) == 0 || !strings.Contains(out.Console[len(out.Console)-1].Text, "truncated") {
		t.Fatalf("missing truncation marker")
	}

	out, _ = adv(t, `
for (let i = 0; i < 20000; i++) { pm.test("t" + i, () => {}); }
pm.test("x".repeat(5000000), () => { throw new Error("y".repeat(5000000)); });
`, func(in *Input) { in.Limits.Timeout = 20 * time.Second; in.Limits.MaxTests = 100 })
	if len(out.Tests) > 101 {
		t.Fatalf("tests not capped: %d", len(out.Tests))
	}
	size := 0
	for _, tt := range out.Tests {
		size += len(tt.Name) + len(tt.Message)
	}
	if size > 2<<20 {
		t.Fatalf("test text not capped: %d", size)
	}
}

func TestAdversarialTimerFlood(t *testing.T) {
	out, _ := adv(t, `for (let i = 0; i < 100000; i++) setTimeout(() => {}, i);`, nil)
	wantKind(t, out, "error")
}

func TestSendRequestGuards(t *testing.T) {
	var sent []string
	mod := func(in *Input) {
		in.ScopeCheck = func(u string) error {
			if strings.Contains(u, "example.com") {
				return nil
			}
			return fmt.Errorf("host not in scope")
		}
		in.Sender = scriptctx.SendFunc(func(_ context.Context, r scriptctx.SendRequest) (scriptctx.SendResponse, error) {
			sent = append(sent, r.URL)
			return scriptctx.SendResponse{Code: 200, Status: "OK", Body: []byte("{}")}, nil
		})
	}
	out, _ := adv(t, `
const targets = ["http://127.0.0.1:9966/api/x", "http://169.254.169.254/latest/meta-data/", "http://localhost:8080/", "http://[::1]:9966/", "http://100.64.0.5/", "https://evil.example.org/"];
const results = [];
for (const u of targets) {
  await new Promise((resolve) => pm.sendRequest(u, (err, res) => { results.push(err ? String(err.message) : "sent"); resolve(); }));
}
pm.test("all refused", () => pm.expect(results.every((r) => r.indexOf("out of scope") === 0)).to.be.true);
`, mod)
	mustPass(t, out)
	if len(sent) != 0 {
		t.Fatalf("out-of-scope sends reached the sender: %v", sent)
	}
	if len(out.Sends) != 6 {
		t.Fatalf("audit log: %+v", out.Sends)
	}

	sent = nil
	out, _ = adv(t, `
let refused = 0, ok = 0;
for (let i = 0; i < 40; i++) {
  await new Promise((resolve) => pm.sendRequest("https://api.example.com/" + i, (err) => { if (err) refused++; else ok++; resolve(); }));
}
pm.test("budget", () => { pm.expect(ok).to.equal(25); pm.expect(refused).to.equal(15); });
`, mod)
	mustPass(t, out)
	if len(sent) != 25 {
		t.Fatalf("sent %d want 25", len(sent))
	}

	out, _ = adv(t, `pm.sendRequest("https://api.example.com/x", () => {});`, func(in *Input) {
		mod(in)
		in.Caps.NetSend = false
	})
	if out.Status != StatusError {
		t.Fatalf("net.send denied should error, got %s", out.Status)
	}
}

func TestCapabilitiesDenied(t *testing.T) {
	out, _ := adv(t, `pm.environment.set("a", 1)`, func(in *Input) { in.Caps = scriptctx.Caps{VarsRead: true} })
	wantKind(t, out, "error")
	out, _ = adv(t, `pm.cookies.get("x")`, func(in *Input) { in.Caps = scriptctx.Caps{VarsRead: true, VarsWrite: true} })
	wantKind(t, out, "error")
	// Secret reads off: secret-named variables read as undefined.
	out, _ = adv(t, `pm.test("hidden", () => pm.expect(pm.environment.get("tok")).to.be.undefined);`, func(in *Input) {
		in.Caps = scriptctx.Caps{VarsRead: true, VarsWrite: true}
		in.Vars.Environment = map[string]any{"tok": "canary-xyz"}
		in.Vars.Secret = []string{"tok"}
	})
	mustPass(t, out)
}

func TestSecretCanaryNeverInHumanOutput(t *testing.T) {
	const canary = "canary-5ecret-VALUE"
	out, _ := adv(t, `
const t = pm.environment.get("tok");
console.log("token=" + t, JSON.stringify({ t }));
console.error(new Error("failed with " + t).message);
pm.test("name " + t, () => { throw new Error("body had " + t); });
throw new Error("fatal " + t);
`, func(in *Input) {
		in.Vars.Environment = map[string]any{"tok": canary}
		in.Vars.Secret = []string{"tok"}
	})
	b, _ := json.Marshal([]any{out.Console, out.Tests, out.Errors})
	if strings.Contains(string(b), canary) {
		t.Fatalf("canary leaked: %s", b)
	}
	if !strings.Contains(string(b), "***") {
		t.Fatalf("expected masking marker: %s", b)
	}
	if got := out.Redact("x " + canary); strings.Contains(got, canary) {
		t.Fatalf("Redact leaked: %q", got)
	}
}

func TestCallerScrubIsApplied(t *testing.T) {
	out, _ := adv(t, `console.log("Bearer abc123")`, func(in *Input) {
		in.Scrub = func(s string) string { return strings.ReplaceAll(s, "abc123", "[redacted]") }
	})
	if out.Console[0].Text != "Bearer [redacted]" {
		t.Fatalf("%+v", out.Console)
	}
}

func TestDeterministicRuns(t *testing.T) {
	src := `
pm.environment.set("u", pm.variables.replaceIn("{{$guid}}"));
pm.environment.set("r", Math.random());
pm.environment.set("t", Date.now());
pm.environment.set("w", CryptoJS.lib.WordArray.random(8).toString());
pm.environment.set("salted", CryptoJS.AES.encrypt("x", "pw").toString());
pm.environment.set("rb", require("crypto").randomBytes(4).toString("hex"));
`
	a, _ := adv(t, src, nil)
	b, _ := adv(t, src, nil)
	mustPass(t, a)
	if !jsonEq(a.Vars.Environment, b.Vars.Environment) {
		t.Fatalf("runs differ:\n%v\n%v", a.Vars.Environment, b.Vars.Environment)
	}
}

func jsonEq(a, b any) bool { return eqJSON(a, b) }

func TestSyntaxErrorReported(t *testing.T) {
	out, _ := adv(t, `pm.test("x", () => {`, nil)
	wantKind(t, out, "syntax")
}

func TestSourceTooLarge(t *testing.T) {
	out, _ := adv(t, strings.Repeat("//x\n", 200000), func(in *Input) { in.Limits.MaxSource = 1 << 10 })
	wantKind(t, out, "limit")
}

func TestContextCancel(t *testing.T) {
	in := base(scriptctx.PhaseTest, `while(true){}`)
	in.Response = jsonResp(200, "{}")
	in.Limits.Timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := Run(ctx, in)
	if time.Since(start) > 3*time.Second || out.Status != StatusError {
		t.Fatalf("cancel not honoured: %s %s", out.Status, time.Since(start))
	}
}

func TestTestsSurviveTimeout(t *testing.T) {
	out, _ := adv(t, `
pm.test("before", () => pm.expect(1).to.equal(1));
pm.environment.set("k", "v");
while (true) {}
`, nil)
	wantKind(t, out, "timeout")
	if len(out.Tests) != 1 || out.Tests[0].Name != "before" {
		t.Fatalf("results before the timeout were lost: %+v (errors %+v)", out.Tests, out.Errors)
	}
	if out.Vars.Environment["k"] != "v" {
		t.Fatalf("var writes before the timeout were lost: %+v", out.Vars.Environment)
	}
}
