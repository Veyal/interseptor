package mcp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type evReq struct {
	method, path, query string
	body                map[string]any
}

func evidenceServer(t *testing.T, got *[]evReq, resp func(r *http.Request) string) *Server {
	t.Helper()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		it := evReq{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&it.body)
		}
		*got = append(*got, it)
		_, _ = w.Write([]byte(resp(r)))
	}))
	t.Cleanup(mock.Close)
	s := New(mock.URL)
	s.report = func(Activity) {}
	return s
}

const evPNG = "\x89PNG\r\n\x1a\nfake"

func evJSON() string {
	b, _ := json.Marshal(map[string]any{
		"png": base64.StdEncoding.EncodeToString([]byte(evPNG)), "alt": "Timeline of 5 requests.",
		"summary": "5 sent, 2 blocked", "kind": "intruder_timeline", "width": 1100, "height": 400,
	})
	return string(b)
}

func TestEvidenceRenderToolsListed(t *testing.T) {
	s := New("http://127.0.0.1:0")
	for _, name := range []string{"render_intruder_preview", "render_evidence"} {
		desc, schema, ok := s.ToolMeta(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if schema["properties"] == nil {
			t.Fatalf("%s has no schema", name)
		}
		l := strings.ToLower(desc)
		if !strings.Contains(l, "not browser proof") || !strings.Contains(desc, "add_finding_image") {
			t.Fatalf("%s description must disclaim browser proof and name add_finding_image: %s", name, desc)
		}
	}
	props := toolProperties(t, s, "render_intruder_preview")
	for _, k := range []string{"attackId", "kind", "findingId", "caption", "role", "proof", "mask"} {
		if props[k] == nil {
			t.Errorf("render_intruder_preview missing %s", k)
		}
	}
	props = toolProperties(t, s, "render_evidence")
	for _, k := range []string{"kind", "findingId", "caption", "runId", "flowIdA", "flowIdB", "flowIds", "findingIds"} {
		if props[k] == nil {
			t.Errorf("render_evidence missing %s", k)
		}
	}
}

func TestRenderIntruderPreviewDataURI(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(*http.Request) string { return evJSON() })
	out, err := s.Call("render_intruder_preview", map[string]any{"kind": "timeline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/api/intruder/attacks/latest/render" {
		t.Fatalf("requests = %+v", got)
	}
	if !strings.Contains(got[0].query, "kind=intruder_timeline") {
		t.Fatalf("query = %q", got[0].query)
	}
	for _, want := range []string{"Timeline of 5 requests.", "5 sent, 2 blocked", "data:image/png;base64,", "not a browser screenshot"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderIntruderPreviewRawPNGFallback(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(*http.Request) string { return evPNG })
	out, err := s.Call("render_intruder_preview", map[string]any{"kind": "race", "attackId": "abc123", "mask": true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/intruder/attacks/abc123/render" || !strings.Contains(got[0].query, "mask=1") {
		t.Fatalf("request = %+v", got[0])
	}
	if !strings.Contains(out, "data:image/png;base64,") {
		t.Fatalf("no data uri: %s", out)
	}
}

func TestRenderIntruderPreviewAttach(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(*http.Request) string { return `{"id":7}` })
	_, err := s.Call("render_intruder_preview", map[string]any{
		"kind": "strip", "attackId": "r1", "findingId": 7, "caption": "c", "role": "result", "proof": "p", "mask": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	g := got[0]
	if g.method != http.MethodPost || g.path != "/api/findings/7/evidence-render" {
		t.Fatalf("request = %+v", g)
	}
	if g.body["kind"] != "intruder_strip" || g.body["attackId"] != "r1" || g.body["caption"] != "c" ||
		g.body["role"] != "result" || g.body["proof"] != "p" || g.body["mask"] != true {
		t.Fatalf("body = %+v", g.body)
	}
}

func TestRenderEvidenceDataURIAndAttach(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(r *http.Request) string {
		if r.Method == http.MethodGet {
			return evJSON()
		}
		return `{"id":9}`
	})
	out, err := s.Call("render_evidence", map[string]any{"kind": "flow_diff", "flowIdA": 4, "flowIdB": 5})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/evidence-render" || !strings.Contains(got[0].query, "kind=flow_diff") ||
		!strings.Contains(got[0].query, "flowIdA=4") || !strings.Contains(got[0].query, "flowIdB=5") {
		t.Fatalf("request = %+v", got[0])
	}
	if !strings.Contains(out, "Timeline of 5 requests.") {
		t.Fatalf("alt missing: %s", out)
	}
	_, err = s.Call("render_evidence", map[string]any{"kind": "finding_chain", "findingIds": []any{1, 2}, "findingId": 9, "caption": "c"})
	if err != nil {
		t.Fatal(err)
	}
	g := got[1]
	if g.method != http.MethodPost || g.path != "/api/findings/9/evidence-render" || g.body["kind"] != "finding_chain" {
		t.Fatalf("request = %+v", g)
	}
	if ids, _ := g.body["findingIds"].([]any); len(ids) != 2 {
		t.Fatalf("findingIds = %+v", g.body["findingIds"])
	}
}

func TestEvidenceRenderInvalidKind(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(*http.Request) string { return "{}" })
	if _, err := s.Call("render_intruder_preview", map[string]any{"kind": "pie"}); err == nil {
		t.Fatal("expected error for bad intruder kind")
	}
	if _, err := s.Call("render_evidence", map[string]any{"kind": "timeline"}); err == nil {
		t.Fatal("expected error for bad evidence kind")
	}
	if _, err := s.Call("render_evidence", map[string]any{}); err == nil {
		t.Fatal("expected error for missing kind")
	}
	if len(got) != 0 {
		t.Fatalf("invalid kinds must not hit the API: %+v", got)
	}
}

func TestStartIntruderDocumentsBarrierAndRunID(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(*http.Request) string { return `{"runId":"x"}` })
	desc, _, _ := s.ToolMeta("start_intruder")
	if !strings.Contains(desc, "barrier") || !strings.Contains(desc, "runId") {
		t.Fatalf("start_intruder description: %s", desc)
	}
	if toolProperties(t, s, "start_intruder")["barrier"] == nil {
		t.Fatal("start_intruder schema missing barrier")
	}
	idesc, _, _ := s.ToolMeta("intruder_state")
	if !strings.Contains(idesc, "runId") {
		t.Fatalf("intruder_state description: %s", idesc)
	}
	if _, err := s.Call("start_intruder", map[string]any{"target": "https://example.com", "template": "GET / HTTP/1.1", "attackType": "repeat", "barrier": true}); err != nil {
		t.Fatal(err)
	}
	if got[0].body["barrier"] != true {
		t.Fatalf("barrier not forwarded: %+v", got[0].body)
	}
}

func TestRenderRequestsAskForInlineJSONAndHandleOmittedPNG(t *testing.T) {
	var got []evReq
	big, _ := json.Marshal(map[string]any{"pngOmitted": true, "bytes": 3 << 20, "alt": "Big render.", "summary": "s", "kind": "intruder_timeline"})
	s := evidenceServer(t, &got, func(*http.Request) string { return string(big) })
	out, err := s.Call("render_intruder_preview", map[string]any{"kind": "timeline", "unmask": true, "expected": 1})
	if err != nil {
		t.Fatal(err)
	}
	q := got[0].query
	for _, want := range []string{"format=json", "png=1", "unmask=1", "expected=1"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q lacks %s", q, want)
		}
	}
	if strings.Contains(out, "data:image/png") || !strings.Contains(out, "over the inline limit") || !strings.Contains(out, "Big render.") {
		t.Fatalf("omitted PNG must return alt, summary and a pointer, not a data URI: %s", out)
	}
}

func TestRenderEvidenceForwardsIncludeBody(t *testing.T) {
	var got []evReq
	s := evidenceServer(t, &got, func(r *http.Request) string {
		if r.Method == http.MethodGet {
			return evJSON()
		}
		return `{"id":1}`
	})
	if _, err := s.Call("render_evidence", map[string]any{"kind": "flow_diff", "flowIdA": 1, "flowIdB": 2, "includeBody": true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0].query, "includeBody=1") {
		t.Fatalf("query %q", got[0].query)
	}
	if _, err := s.Call("render_evidence", map[string]any{"kind": "flow_diff", "flowIdA": 1, "flowIdB": 2, "includeBody": true, "findingId": 3}); err != nil {
		t.Fatal(err)
	}
	if got[1].body["includeBody"] != true {
		t.Fatalf("body %+v", got[1].body)
	}
}
