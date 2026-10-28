package mcp

import (
	"net/url"
	"testing"
)

// Loose clients send booleans as strings and lines as an array; neither may be
// silently dropped or mangled.
func TestRenderFlowPreviewAcceptsStringBoolAndLinesArray(t *testing.T) {
	var q url.Values
	var body map[string]any
	mock := focusMock(t, &q, &body)
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("render_flow_preview", map[string]any{
		"flowId": 9, "findingId": 3, "focus": `a.b`, "regex": "true", "lines": []any{"res:400-440", "req:1-5"},
	}); err != nil {
		t.Fatal(err)
	}
	if body["regex"] != true || body["lines"] != "res:400-440,req:1-5" {
		t.Fatalf("attach body = %#v", body)
	}
}

func TestArgBoolAcceptsStringForms(t *testing.T) {
	for _, tc := range []struct {
		v    any
		def  bool
		want bool
	}{
		{true, false, true}, {"true", false, true}, {"TRUE", false, true}, {" 1 ", false, true}, {float64(1), false, true},
		{"false", true, false}, {"0", true, false}, {float64(0), true, false}, {"bogus", true, true}, {nil, true, true},
	} {
		if got := argBool(map[string]any{"k": tc.v}, "k", tc.def); got != tc.want {
			t.Errorf("argBool(%#v, def=%v) = %v, want %v", tc.v, tc.def, got, tc.want)
		}
	}
}
