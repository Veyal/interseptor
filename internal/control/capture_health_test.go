package control

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// TestCaptureCheckReportsDrops pins that capture loss is never reported as
// healthy: any dropped flow write or WebSocket frame fails the readiness row
// with the counts and a fix.
func TestCaptureCheckReportsDrops(t *testing.T) {
	if ok, detail, fix := captureCheck(store.CaptureStats{}); !ok || fix != "" || detail == "" {
		t.Fatalf("clean capture = %v %q %q", ok, detail, fix)
	}
	ok, detail, fix := captureCheck(store.CaptureStats{FlowWritesDropped: 12, WSFramesDropped: 3})
	if ok {
		t.Fatal("dropped writes reported as healthy")
	}
	for _, want := range []string{"12 flow write(s)", "3 WebSocket frame(s)", "History is incomplete"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q missing %q", detail, want)
		}
	}
	if fix == "" {
		t.Error("lossy capture has no fix hint")
	}
	if ok, detail, _ := captureCheck(store.CaptureStats{WSFramesDropped: 1}); ok || strings.Contains(detail, "flow write") {
		t.Fatalf("ws-only loss = %v %q", ok, detail)
	}
}

func TestReadinessAndProjectAggregateExposeCapture(t *testing.T) {
	h, _, _ := newHub(t)
	rep := (&authzAPI{h}).buildReadiness()
	var found bool
	for _, c := range rep.Checks {
		if c.ID == "capture" {
			found = true
			if !c.OK {
				t.Fatalf("fresh store capture check = %+v, want ok", c)
			}
		}
	}
	if !found {
		t.Fatal("readiness missing capture check")
	}
	_, out, raw := getProjectReadiness(t, h)
	if !strings.Contains(raw, `"capture":{"flowWritesDropped":0,"wsFramesDropped":0}`) {
		t.Fatalf("project readiness missing capture counters: %s", raw)
	}
	if out.Capture.FlowWritesDropped != 0 || out.Capture.WSFramesDropped != 0 {
		t.Fatalf("capture = %+v", out.Capture)
	}
}
