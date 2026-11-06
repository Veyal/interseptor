package mcp

import (
	"math"
	"strings"
	"testing"
)

// JSON has one number type, so a client that sends 2.5 for a flow id reaches the
// tool handler as float64. The old reqInt/argInt truncated it to 2 — silently
// addressing a *different* flow — and converted 1e300 (or ±Inf) with Go's
// implementation-defined float→int conversion, producing a garbage id. Every
// integer argument must therefore be rejected (required) or fall back to its
// default (optional) unless it is a whole number in int range.

func TestReqIntRejectsFractionalFloat(t *testing.T) {
	_, err := reqInt(map[string]any{"flowId": 2.5}, "flowId")
	if err == nil {
		t.Fatal("reqInt accepted 2.5; a fractional id must be an error, not a silent truncation")
	}
	if !strings.Contains(err.Error(), "must be an integer") || !strings.Contains(err.Error(), "2.5") {
		t.Fatalf("message should name the type and echo the value: %q", err)
	}
}

func TestReqIntRejectsOutOfRangeAndNonFinite(t *testing.T) {
	for _, bad := range []any{1e300, -1e300, math.Inf(1), math.NaN(), 9.3e18} {
		if n, err := reqInt(map[string]any{"id": bad}, "id"); err == nil {
			t.Fatalf("reqInt accepted %v (got %d); out-of-range values must not convert to an id", bad, n)
		}
	}
	// Whole, in-range floats and numeric strings still work.
	if n, err := reqInt(map[string]any{"id": float64(9)}, "id"); err != nil || n != 9 {
		t.Fatalf("json number: %d %v", n, err)
	}
	if n, err := reqInt(map[string]any{"id": "7"}, "id"); err != nil || n != 7 {
		t.Fatalf("numeric string: %d %v", n, err)
	}
}

func TestArgIntFallsBackForUnusableNumbers(t *testing.T) {
	if got := argInt(map[string]any{"limit": 1e300}, "limit", 100); got != 100 {
		t.Fatalf("huge float limit = %d, want the default 100", got)
	}
	if got := argInt(map[string]any{"limit": 2.5}, "limit", 100); got != 100 {
		t.Fatalf("fractional limit = %d, want the default 100", got)
	}
	if got := argInt(map[string]any{"limit": math.Inf(-1)}, "limit", 100); got != 100 {
		t.Fatalf("infinite limit = %d, want the default 100", got)
	}
	if got := argInt(map[string]any{"limit": float64(7)}, "limit", 100); got != 7 {
		t.Fatalf("whole json number = %d, want 7", got)
	}
	if got := argInt(map[string]any{"limit": "8"}, "limit", 100); got != 8 {
		t.Fatalf("numeric string = %d, want 8", got)
	}
}

func TestReqFlowIDRejectsFractionalID(t *testing.T) {
	if _, err := reqFlowID(map[string]any{"flowId": 3.5}); err == nil {
		t.Fatal("reqFlowID accepted 3.5")
	}
	if n, err := reqFlowID(map[string]any{"flowId": float64(4)}); err != nil || n != 4 {
		t.Fatalf("reqFlowID(4) = %d %v", n, err)
	}
}

// The rejection must reach the AI as an isError tool result that echoes the
// value, and must not fall through to the control API with a truncated id.
func TestHugeFlowIDReachesToolError(t *testing.T) {
	srv := New("http://127.0.0.1:1") // never contacted: validation fails first
	res := srv.callTool([]byte(`{"name":"analyze_flow","arguments":{"flowId":1e300}}`))
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	if m["isError"] != true {
		t.Fatalf("out-of-range id should be a tool error: %v", m)
	}
	content, _ := m["content"].([]map[string]any)
	if len(content) == 0 {
		t.Fatalf("no content: %v", m)
	}
	text, _ := content[0]["text"].(string)
	if !strings.Contains(text, "must be an integer") || !strings.Contains(text, "1e+300") {
		t.Fatalf("tool error should explain the range and echo the value: %q", text)
	}
}
