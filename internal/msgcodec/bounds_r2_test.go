package msgcodec

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func r2Lower[T any](t *testing.T, v *T, to T) {
	t.Helper()
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

// r2Within runs fn and fails if it has not returned after d. The step budget
// bounds interpreter steps, not time: one step can be a slow builtin.
func r2Within(t *testing.T, d time.Duration, fn func()) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() { defer close(done); fn() }()
	select {
	case <-done:
		return time.Since(start)
	case <-time.After(d):
		t.Fatalf("codec call still running after %s", d)
		return 0
	}
}

// 5M steps of hashing a 1 MiB body is minutes of CPU inside the step budget.
const r2SlowCodec = `
def match(flow, side):
    return True

def decode(flow, side, raw):
    x = ""
    for i in range(1000000):
        x = sha256_hex(raw)
    return x

def encode(flow, side, plaintext):
    x = ""
    for i in range(1000000):
        x = sha256_hex(plaintext)
    return x
`

func TestR2CodecCallHasAWallClockBound(t *testing.T) {
	r2Lower(t, &callTimeout, 300*time.Millisecond)
	c, err := Compile("slow", r2SlowCodec)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("a", 1<<20)
	var res Result
	var derr error
	took := r2Within(t, 10*time.Second, func() { res, derr = c.Decode(Flow{ReqBody: body}, "req") })
	if derr == nil || !strings.Contains(derr.Error(), "time limit") {
		t.Fatalf("decode: err = %v, want a time-limit error (took %s)", derr, took)
	}
	if !strings.Contains(res.Error, "time limit") {
		t.Errorf("result error should name the limit: %q", res.Error)
	}
	if took > 3*time.Second {
		t.Errorf("decode took %s with a 300ms limit", took)
	}
	took = r2Within(t, 10*time.Second, func() { _, derr = c.Encode(Flow{}, "req", body) })
	if derr == nil || !strings.Contains(derr.Error(), "time limit") {
		t.Fatalf("encode: err = %v (took %s)", derr, took)
	}
}

// Top-level code runs at compile time and is bounded the same way.
func TestR2CompileHasAWallClockBound(t *testing.T) {
	r2Lower(t, &callTimeout, 300*time.Millisecond)
	src := "big = \"a\" * (1 << 20)\nx = [sha256_hex(big) for i in range(1000000)]\n" + r2SlowCodec
	var err error
	took := r2Within(t, 10*time.Second, func() { _, err = Compile("slow-top", src) })
	if err == nil || !strings.Contains(err.Error(), "time limit") {
		t.Fatalf("compile: err = %v (took %s)", err, took)
	}
}

// String doubling costs a handful of steps per iteration, so the step budget
// does not bound it: 30 iterations is 1 GiB and nothing in Starlark stops the
// next one. Scaled down here (limit 32 MiB, script heads for 1 GiB) so a
// failing run cannot take the machine down.
const r2DoublingCodec = `
def match(flow, side):
    return True

def decode(flow, side, raw):
    s = "x" * 1024
    for i in range(20):
        s = s + s
    return str(len(s))
`

func TestR2RunawayAllocationIsStopped(t *testing.T) {
	r2Lower(t, &callMemory, 32<<20)
	c, err := Compile("doubling", r2DoublingCodec)
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var derr error
	took := r2Within(t, 20*time.Second, func() { _, derr = c.Decode(Flow{}, "req") })
	runtime.ReadMemStats(&after)
	if derr == nil || !strings.Contains(derr.Error(), "memory limit") {
		t.Fatalf("err = %v, want a memory-limit error (took %s)", derr, took)
	}
	// Unbounded, the script allocates 2 GiB in total (1 KiB doubled 20 times).
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512<<20 {
		t.Errorf("call allocated %d MiB before it was stopped (limit 32 MiB)", allocated>>20)
	}
}

func TestR2ResultSizeIsCapped(t *testing.T) {
	r2Lower(t, &maxResultBytes, 64<<10)
	src := `
def match(flow, side):
    return True

def decode(flow, side, raw):
    if raw == "fields":
        return {"plaintext": "ok", "fields": {"a": "x" * 40000, "b": "y" * 40000}}
    return "x" * (1 << 20)

def encode(flow, side, plaintext):
    return "x" * (1 << 20)
`
	c, err := Compile("big", src)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := c.Decode(Flow{}, "req"); err == nil || !strings.Contains(err.Error(), "result exceeds") || res.Plaintext != "" {
		t.Errorf("decode: err = %v, plaintext %d bytes", err, len(res.Plaintext))
	}
	if res, err := c.Decode(Flow{ReqBody: "fields"}, "req"); err == nil || !strings.Contains(err.Error(), "result exceeds") || len(res.Fields) != 0 {
		t.Errorf("decode fields: err = %v, %d fields", err, len(res.Fields))
	}
	if out, err := c.Encode(Flow{}, "req", "p"); err == nil || !strings.Contains(err.Error(), "result exceeds") || out != "" {
		t.Errorf("encode: err = %v, %d bytes", err, len(out))
	}
	// TryDecode surfaces the failure on the result rather than a huge plaintext.
	if res, ok := TryDecode([]*Codec{c}, Flow{}, "req"); !ok || res.Error == "" || res.Plaintext != "" {
		t.Errorf("TryDecode: ok=%v error=%q plaintext=%d", ok, res.Error, len(res.Plaintext))
	}
}

// Ordinary codecs are unaffected by the bounds.
func TestR2BoundsLeaveOrdinaryCodecsAlone(t *testing.T) {
	src := `
meta = {"apply_on_send": True}

def match(flow, side):
    return flow.host == "example.com"

def decode(flow, side, raw):
    return b64decode(raw)

def encode(flow, side, plaintext):
    return b64encode(plaintext)
`
	c, err := Compile("b64", src)
	if err != nil {
		t.Fatal(err)
	}
	f := Flow{Host: "example.com", ReqBody: "aGVsbG8="}
	if ok, err := c.Match(f, "req"); err != nil || !ok {
		t.Fatalf("match: %v %v", ok, err)
	}
	if res, err := c.Decode(f, "req"); err != nil || res.Plaintext != "hello" {
		t.Fatalf("decode: %+v %v", res, err)
	}
	if out, err := c.Encode(f, "req", "hello"); err != nil || out != "aGVsbG8=" {
		t.Fatalf("encode: %q %v", out, err)
	}
}

func r2Source(tag string) string {
	return "# " + tag + strings.Repeat("-", 512<<10) + "\ndef match(flow, side):\n    return True\n\ndef decode(flow, side, raw):\n    return \"" + tag + "\"\n"
}

// A reader (LoadDir on another request, or a crash) must never observe a
// half-written codec: Save replaces the file in one step.
func TestR2SaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	a, b := r2Source("alpha"), r2Source("beta")
	if err := Save(dir, "c1", a); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var torn, reads int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := os.ReadFile(filepath.Join(dir, "c1.star"))
			reads++
			if err != nil || (string(got) != a && string(got) != b) {
				torn++
			}
		}
	}()
	for i := 0; i < 60; i++ {
		src := a
		if i%2 == 0 {
			src = b
		}
		if err := Save(dir, "c1", src); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if torn > 0 {
		t.Errorf("%d of %d concurrent reads saw a missing, empty or partial codec file", torn, reads)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "c1.star" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Save left other files behind: %v", names)
	}
	if got, err := Read(dir, "c1"); err != nil || got != a {
		t.Errorf("final content is not the last save (err %v)", err)
	}
	if st, err := os.Stat(filepath.Join(dir, "c1.star")); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 (err %v)", st.Mode().Perm(), err)
	}
}

// A source that does not compile must leave the saved codec untouched.
func TestR2FailedSaveKeepsThePreviousCodec(t *testing.T) {
	dir := t.TempDir()
	a := r2Source("alpha")
	if err := Save(dir, "c1", a); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, "c1", "def match(:"); err == nil {
		t.Fatal("invalid source saved")
	}
	if got, _ := Read(dir, "c1"); got != a {
		t.Fatal("previous codec was changed by a failed save")
	}
}
