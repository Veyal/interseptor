package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// focusMock answers preview.png and flow-preview with focus headers.
func focusMock(t *testing.T, gotQuery *url.Values, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/preview.png"):
			*gotQuery = r.URL.Query()
			w.Header().Set("X-Interseptor-Focus-Mode", "top")
			w.Header().Set("X-Interseptor-Focus-Matches", "req=0,res=0")
			w.Header().Set("X-Interseptor-Focus-Notfound", "%22isAdmin%22%3Atrue,res:900-950")
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, "\x89PNG")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/flow-preview"):
			json.NewDecoder(r.Body).Decode(gotBody)
			w.Header().Set("X-Interseptor-Focus-Mode", "focus")
			w.Header().Set("X-Interseptor-Focus-Matches", "req=0,res=2")
			io.WriteString(w, `{"id":3,"title":"t","blocks":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRenderFlowPreviewForwardsFocusAndReportsMatches(t *testing.T) {
	var q url.Values
	var body map[string]any
	mock := focusMock(t, &q, &body)
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	out, err := s.Call("render_flow_preview", map[string]any{
		"flowId": 9, "findingId": 3, "focus": `"isAdmin":true`, "focusSide": "res",
		"lines": "res:400-440", "context": float64(3), "regex": false, "caption": "Highlighted: admin flag",
	})
	if err != nil {
		t.Fatal(err)
	}
	focus, _ := body["focus"].([]any)
	if len(focus) != 1 || focus[0] != `"isAdmin":true` || body["focusSide"] != "res" || body["lines"] != "res:400-440" || body["context"] != float64(3) || body["caption"] != "Highlighted: admin flag" {
		t.Fatalf("attach body = %#v", body)
	}
	if _, ok := body["regex"]; ok {
		t.Fatalf("regex=false should not be forwarded: %#v", body)
	}
	if !strings.Contains(out, "Focus: mode=focus, matches req=0 res=2.") {
		t.Fatalf("result should report matches:\n%s", out)
	}
}

func TestRenderFlowPreviewReportsNotFoundWithRetryAdvice(t *testing.T) {
	var q url.Values
	var body map[string]any
	mock := focusMock(t, &q, &body)
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	out, err := s.Call("render_flow_preview", map[string]any{
		"flowId": 9, "focus": []any{`"isAdmin":true`, "a, b"}, "regex": true, "lines": "res:900-950", "context": float64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(q["focus"], "|") != `"isAdmin":true|a, b` || q.Get("regex") != "1" || q.Get("lines") != "res:900-950" || q.Get("context") != "0" {
		t.Fatalf("query = %v", q)
	}
	for _, want := range []string{`Not found: "\"isAdmin\":true", "res:900-950".`, "render again", "matches req=0 res=0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result missing %q:\n%s", want, out)
		}
	}
}

func TestRenderFlowPreviewDescriptionTeachesFocus(t *testing.T) {
	for _, want := range []string{"first 80 lines", "focus", "lines", "distinctive", "caption", "not found"} {
		if !strings.Contains(renderFlowPreviewDesc, want) {
			t.Fatalf("description should mention %q", want)
		}
	}
}

func TestRenderFlowPreviewSchemaAndForwardingIncludeMode(t *testing.T) {
	s := New("http://127.0.0.1:1")
	props := s.tools["render_flow_preview"].schema["properties"].(map[string]any)
	if _, ok := props["mode"]; !ok {
		t.Fatal("render_flow_preview schema lacks mode")
	}
	var q url.Values
	var body map[string]any
	mock := focusMock(t, &q, &body)
	defer mock.Close()
	s = New(mock.URL)
	s.report = func(Activity) {}
	if _, err := s.Call("render_flow_preview", map[string]any{"flowId": 9, "focus": "x", "mode": "top"}); err != nil {
		t.Fatal(err)
	}
	if q.Get("mode") != "top" {
		t.Fatalf("GET query lacks mode: %v", q)
	}
	if _, err := s.Call("render_flow_preview", map[string]any{"flowId": 9, "findingId": 3, "focus": "x", "mode": "focus"}); err != nil {
		t.Fatal(err)
	}
	if body["mode"] != "focus" {
		t.Fatalf("attach body lacks mode: %#v", body)
	}
}

func TestRenderFlowPreviewAttachNotFoundGivesRetryGuidance(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":"focus not found: isAdmin","notFound":["isAdmin"],"bodyTruncated":["res"]}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	_, err := s.Call("render_flow_preview", map[string]any{"flowId": 9, "findingId": 3, "focus": "isAdmin"})
	if err == nil {
		t.Fatal("a 422 must surface as an error")
	}
	for _, want := range []string{"Nothing was attached", "get_flow", "mode=top", "cut at 64 KiB", "lines="} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestFocusReportExplainsTruncationAndUndrawnMatches(t *testing.T) {
	h := http.Header{}
	h.Set("X-Interseptor-Focus-Mode", "top")
	h.Set("X-Interseptor-Focus-Matches", "req=0,res=0")
	h.Set("X-Interseptor-Focus-Notfound", "isAdmin")
	h.Set("X-Interseptor-Body-Truncated", "res")
	out := focusReport(h)
	if !strings.Contains(out, "the body was cut at 64 KiB for the image; the text may lie beyond it") || strings.Contains(out, "render again") {
		t.Fatalf("truncated not-found must not advise a blind retry:\n%s", out)
	}
	h = http.Header{}
	h.Set("X-Interseptor-Focus-Mode", "focus")
	h.Set("X-Interseptor-Focus-Matches", "req=0,res=3")
	h.Set("X-Interseptor-Focus-Notshown", "res")
	if out := focusReport(h); !strings.Contains(out, "none of the res matches fit in the image") {
		t.Fatalf("undrawn matches must be reported:\n%s", out)
	}
	h = http.Header{}
	h.Set("X-Interseptor-Focus-Mode", "top")
	h.Set("X-Interseptor-Focus-Matches", "req=0,res=0")
	h.Set("X-Interseptor-Focus-Masked", "1")
	if out := focusReport(h); !strings.Contains(out, "masked") {
		t.Fatalf("masked-only focus must be reported:\n%s", out)
	}
}

func TestRenderFlowPreviewDecodeSchemaForwardingAndReport(t *testing.T) {
	s := New("http://127.0.0.1:1")
	props := s.tools["render_flow_preview"].schema["properties"].(map[string]any)
	if _, ok := props["decode"]; !ok {
		t.Fatal("render_flow_preview schema lacks decode")
	}
	for _, want := range []string{"decode=auto", "decode=raw", "wire format", "decoded body", "original bytes"} {
		if !strings.Contains(renderFlowPreviewDesc, want) {
			t.Fatalf("description should mention %q", want)
		}
	}

	var q url.Values
	var body map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Interseptor-Decoded-Body", "req=codec:example-envelope,res=base64")
		if r.Method == http.MethodGet {
			q = r.URL.Query()
			w.Header().Set("Content-Type", "image/png")
			io.WriteString(w, "\x89PNG")
			return
		}
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"id":3,"title":"t","blocks":[]}`)
	}))
	defer mock.Close()
	s = New(mock.URL)
	s.report = func(Activity) {}

	out, err := s.Call("render_flow_preview", map[string]any{"flowId": 9, "decode": "RAW"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("decode") != "raw" {
		t.Fatalf("GET query lacks decode=raw: %v", q)
	}
	for _, want := range []string{"Decoded body: req=codec:example-envelope, res=base64", "decode=raw"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result should mention %q:\n%s", want, out)
		}
	}
	out, err = s.Call("render_flow_preview", map[string]any{"flowId": 9, "findingId": 3, "decode": "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if body["decode"] != "raw" || !strings.Contains(out, "Decoded body: req=codec:example-envelope") {
		t.Fatalf("attach body %#v\n%s", body, out)
	}
	if _, err := s.Call("render_flow_preview", map[string]any{"flowId": 9}); err != nil {
		t.Fatal(err)
	}
	if _, ok := q["decode"]; ok {
		t.Fatalf("decode must not be sent when unset: %v", q)
	}
	if _, err := s.Call("render_flow_preview", map[string]any{"flowId": 9, "decode": "rot13"}); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("invalid decode must be rejected, got %v", err)
	}
}
