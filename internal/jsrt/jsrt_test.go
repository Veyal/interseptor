package jsrt

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

func eval(t *testing.T, o Options, src string) (Result, error) {
	t.Helper()
	rt := New(o)
	defer rt.Close()
	return rt.Eval(context.Background(), "t.js", src)
}

func TestSpikeLanguageFeatures(t *testing.T) {
	cases := map[string]struct {
		src  string
		want any
	}{
		"optional chaining":   {`({a:null}).a?.b ?? "d"`, "d"},
		"nullish":             {`null ?? 7`, int64(7)},
		"class":               {`class A{#p=2; get v(){return this.#p*2}}; new A().v`, int64(4)},
		"lookbehind":          {`"price: $42".match(/(?<=\$)\d+/)[0]`, "42"},
		"named groups":        {`"2024-05".match(/(?<y>\d{4})/).groups.y`, "2024"},
		"async await":         {`(async()=>{ const x = await Promise.resolve(3); return x+1 })()`, int64(4)},
		"promise all":         {`Promise.all([1,Promise.resolve(2)]).then(a=>a.length)`, int64(2)},
		"destructuring":       {`const {a,...r}={a:1,b:2,c:3}; Object.keys(r).join()`, "b,c"},
		"spread/template":     {"`${[...'ab'].length}`", "2"},
		"json fidelity":       {`JSON.stringify(JSON.parse('{"a":[1,2,{"b":null}],"c":1.5e3}'))`, `{"a":[1,2,{"b":null}],"c":1500}`},
		"date parse":          {`new Date("2024-01-02T03:04:05Z").getTime()`, int64(1704164645000)},
		"bigint":              {`(2n**64n).toString()`, "18446744073709551616"},
		"generators":          {`function*g(){yield 1;yield 2}; [...g()].length`, int64(2)},
		"top-level await-ish": {`(async()=>{ await null; return "ok" })()`, "ok"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := eval(t, Options{}, c.src)
			if err != nil {
				t.Fatal(err)
			}
			if r.Value != c.want {
				t.Fatalf("got %#v want %#v", r.Value, c.want)
			}
		})
	}
}

func TestInterruptSyncLoop(t *testing.T) {
	start := time.Now()
	_, err := eval(t, Options{Timeout: 100 * time.Millisecond}, `while(true){}`)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("interrupt too slow")
	}
}

func TestInterruptInsidePromiseLoop(t *testing.T) {
	src := `function f(){ return Promise.resolve().then(f) } f(); 0`
	start := time.Now()
	_, err := eval(t, Options{Timeout: 150 * time.Millisecond}, src)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("promise-job loop not interrupted promptly")
	}
}

func TestInterruptInsideAsyncLoop(t *testing.T) {
	src := `(async()=>{ for(;;){ await null } })()`
	_, err := eval(t, Options{Timeout: 150 * time.Millisecond}, src)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
}

func TestInterruptInsideTimerLoop(t *testing.T) {
	src := `function f(){ while(true){} } setTimeout(f, 5); 0`
	_, err := eval(t, Options{Timeout: 150 * time.Millisecond}, src)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
}

func TestContextCancel(t *testing.T) {
	rt := New(Options{Timeout: 30 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := rt.Eval(ctx, "t", `while(true){}`)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestDeterministicClockAndRand(t *testing.T) {
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	run := func() Result {
		src := `[Date.now(), new Date().toISOString(), Math.random(), Math.random(), performance.now()].join("|")`
		r, err := eval(t, Options{
			Clock: func() time.Time { return fixed },
			Rand:  rand.New(rand.NewPCG(1, 2)).Float64,
		}, src)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := run(), run()
	if a.Value != b.Value {
		t.Fatalf("not deterministic: %v vs %v", a.Value, b.Value)
	}
	if !strings.HasPrefix(a.Value.(string), "1893553445000|2030-01-02T03:04:05.000Z|") {
		t.Fatalf("clock not injected: %v", a.Value)
	}
}

func TestVirtualTimersDoNotSleep(t *testing.T) {
	fixed := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	src := `
var log=[]; var t0=Date.now();
setTimeout(()=>log.push("b:"+(Date.now()-t0)), 2000);
setTimeout(()=>log.push("a:"+(Date.now()-t0)), 1000);
(async()=>{ await new Promise(r=>setTimeout(r,5000)); log.push("c:"+(Date.now()-t0)); return log.join(",") })()`
	start := time.Now()
	r, err := eval(t, Options{Clock: func() time.Time { return fixed }}, src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "a:1000,b:2000,c:5000" {
		t.Fatalf("got %v", r.Value)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timers really slept")
	}
}

func TestTimerFloodAndVirtualSpan(t *testing.T) {
	_, err := eval(t, Options{MaxTimers: 50}, `for(let i=0;i<100;i++) setTimeout(()=>{},1)`)
	if err == nil {
		t.Fatal("expected timer cap error")
	}
	_, err = eval(t, Options{MaxTimers: 50}, `setInterval(()=>{},1)`)
	if !errors.Is(err, ErrTimers) {
		t.Fatalf("interval flood err=%v", err)
	}
	r, err := eval(t, Options{}, `var n=0; setTimeout(()=>{n++}, 120000); n`)
	if err != nil || r.Value != int64(0) {
		t.Fatalf("beyond virtual span must not fire: %v %v", r.Value, err)
	}
}

func TestConsoleCaptureAndCap(t *testing.T) {
	r, err := eval(t, Options{ConsoleMax: 30}, `console.log("hi", {a:1}); console.error("x".repeat(100)); console.log("dropped")`)
	if err != nil {
		t.Fatal(err)
	}
	if !r.ConsoleTruncated || len(r.Console) < 2 || r.Console[0].Text != `hi {"a":1}` {
		t.Fatalf("console=%+v trunc=%v", r.Console, r.ConsoleTruncated)
	}
	if r.Console[1].Level != "error" {
		t.Fatalf("level=%s", r.Console[1].Level)
	}
	total := 0
	for _, c := range r.Console {
		total += len(c.Text)
	}
	if total > 30 {
		t.Fatalf("console exceeded cap: %d", total)
	}
}

func TestNoAmbientIO(t *testing.T) {
	for _, g := range []string{"require", "process", "fetch", "XMLHttpRequest", "fs", "child_process", "module", "Buffer", "WebSocket", "os"} {
		r, err := eval(t, Options{}, `typeof `+g)
		if err != nil || r.Value != "undefined" {
			t.Fatalf("%s leaked: %v %v", g, r.Value, err)
		}
	}
	_, err := eval(t, Options{}, `require("fs")`)
	var se *ScriptError
	if !errors.As(err, &se) {
		t.Fatalf("want ScriptError, got %v", err)
	}
}

func TestScriptErrorsAndLimits(t *testing.T) {
	var se *ScriptError
	if _, err := eval(t, Options{}, `throw new Error("boom")`); !errors.As(err, &se) || !strings.Contains(se.Message, "boom") {
		t.Fatalf("err=%v", err)
	}
	if _, err := eval(t, Options{}, `var ;`); !errors.As(err, &se) {
		t.Fatalf("syntax err=%v", err)
	}
	if _, err := eval(t, Options{}, `function f(){f()} f()`); !errors.As(err, &se) {
		t.Fatalf("stack overflow err=%v", err)
	}
	if _, err := eval(t, Options{MaxSource: 10}, strings.Repeat("1;", 20)); !errors.Is(err, ErrSourceSize) {
		t.Fatalf("err=%v", err)
	}
	if _, err := eval(t, Options{}, `Promise.reject(new Error("nope"))`); !errors.As(err, &se) {
		t.Fatalf("unhandled rejection err=%v", err)
	}
}

func TestSetGlobalsAndFunc(t *testing.T) {
	rt := New(Options{})
	if err := rt.Set("pm", map[string]any{
		"base": "example.com",
		"up":   Func(func(a []any) (any, error) { return strings.ToUpper(a[0].(string)), nil }),
		"bad":  Func(func(a []any) (any, error) { return nil, errors.New("denied") }),
	}); err != nil {
		t.Fatal(err)
	}
	r, err := rt.Eval(context.Background(), "t", `var m; try{pm.bad()}catch(e){m=e.message} pm.up(pm.base)+"|"+m`)
	if err != nil || r.Value != "EXAMPLE.COM|denied" {
		t.Fatalf("%v %v", r.Value, err)
	}
}

func TestPrototypePollutionIsolatedPerRuntime(t *testing.T) {
	if _, err := eval(t, Options{}, `Object.prototype.polluted = 1`); err != nil {
		t.Fatal(err)
	}
	r, err := eval(t, Options{}, `typeof ({}).polluted`)
	if err != nil || r.Value != "undefined" {
		t.Fatalf("pollution leaked across runtimes: %v %v", r.Value, err)
	}
}

func TestHeavyAllocationDoesNotCrashWithinTimeout(t *testing.T) {
	// Documents the known goja limitation: no heap cap. A bounded String.repeat
	// is fine; unbounded growth is handled by the worker (WP9), not here.
	r, err := eval(t, Options{}, `"a".repeat(1<<20).length`)
	if err != nil || r.Value != int64(1<<20) {
		t.Fatalf("%v %v", r.Value, err)
	}
}

func TestEvalUsableAfterInterrupt(t *testing.T) {
	rt := New(Options{Timeout: 50 * time.Millisecond})
	defer rt.Close()
	if _, err := rt.Eval(context.Background(), "a.js", `globalThis.keep = 7; while(true){}`); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
	r, err := rt.Eval(context.Background(), "b.js", `globalThis.keep`)
	if err != nil || r.Value != int64(7) {
		t.Fatalf("runtime unusable after interrupt: %v %v", r.Value, err)
	}
}
