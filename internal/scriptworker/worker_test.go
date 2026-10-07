package scriptworker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/pmsandbox"
	"github.com/Veyal/interseptor/internal/scriptctx"
)

func runFrame(t testing.TB, mut func(*runWire)) []byte {
	t.Helper()
	w := runWire{V: ProtocolVersion, Phase: scriptctx.PhaseTest, Script: `pm.test("ok", function(){})`, Caps: scriptctx.DefaultCaps()}
	if mut != nil {
		mut(&w)
	}
	b, _ := json.Marshal(w)
	var buf bytes.Buffer
	_ = WriteFrame(&buf, FrameRun, b)
	return buf.Bytes()
}

func TestServeRunsScriptOverPipes(t *testing.T) {
	var out bytes.Buffer
	pr, pw := io.Pipe() // the parent keeps stdin open while the run is in flight
	defer pw.Close()
	go pw.Write(runFrame(t, nil))
	if err := Serve(pr, &out, WorkerConfig{}); err != nil {
		t.Fatal(err)
	}
	typ, p, err := ReadFrame(bufio.NewReader(&out))
	if err != nil || typ != FrameOutput {
		t.Fatalf("got %c %v", typ, err)
	}
	var o pmsandbox.Output
	if err := json.Unmarshal(p, &o); err != nil || o.Status != pmsandbox.StatusPass || len(o.Tests) != 1 {
		t.Fatalf("output %+v err %v", o, err)
	}
}

func TestServeRejectsProtocolViolations(t *testing.T) {
	cases := map[string][]byte{
		"empty":        nil,
		"wrong first":  {0, 0, 0, 2, FrameReply, '{', '}'},
		"bad json":     {0, 0, 0, 3, FrameRun, 'x', 'y', 'z'},
		"bad version":  runFrame(t, func(w *runWire) { w.V = 99 }),
		"huge length":  {0xff, 0xff, 0xff, 0xff, FrameRun},
		"unknown type": {0, 0, 0, 0, 'Q'},
	}
	for name, in := range cases {
		if err := Serve(bytes.NewReader(in), io.Discard, WorkerConfig{}); err == nil {
			t.Errorf("%s: Serve accepted it", name)
		}
	}
}

func FuzzServe(f *testing.F) {
	f.Add(runFrame(f, nil))
	f.Add(runFrame(f, func(w *runWire) { w.Script = `throw new Error("x")`; w.Limits.Timeout = time.Second }))
	f.Add(runFrame(f, func(w *runWire) { w.Response = &responseWire{Code: 200, Body: []byte("{}")} }))
	f.Add([]byte{0, 0, 0, 2, FrameRun, '{', '}'})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 'R'})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			t.Skip()
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = Serve(bytes.NewReader(data), io.Discard, WorkerConfig{})
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve hung")
		}
	})
}
