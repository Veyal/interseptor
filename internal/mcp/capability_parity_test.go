package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func toolProperties(t *testing.T, s *Server, name string) map[string]any {
	t.Helper()
	_, schema, ok := s.ToolMeta(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no object properties: %#v", name, schema)
	}
	return props
}

func TestResponseInterceptToolsMirrorREST(t *testing.T) {
	type received struct {
		method string
		path   string
		body   map[string]any
	}
	var got []received
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		item := received{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&item.body)
		}
		got = append(got, item)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}
	for _, name := range []string{"forward_response", "drop_response"} {
		props := toolProperties(t, s, name)
		if _, ok := props["id"]; !ok {
			t.Errorf("%s schema missing id", name)
		}
	}
	if props := toolProperties(t, s, "forward_response"); props["raw"] == nil {
		t.Error("forward_response schema missing optional raw response")
	}

	if _, err := s.Call("forward_response", map[string]any{"id": 12, "raw": "HTTP/1.1 204 No Content\r\n\r\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("drop_response", map[string]any{"id": 13}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d REST calls, want 2", len(got))
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/intercept/response/12/forward" ||
		got[0].body["raw"] != "HTTP/1.1 204 No Content\r\n\r\n" {
		t.Fatalf("forward_response REST call = %#v", got[0])
	}
	if got[1].method != http.MethodPost || got[1].path != "/api/intercept/response/13/drop" {
		t.Fatalf("drop_response REST call = %#v", got[1])
	}
}

func TestRuleToolsAcceptSideAndPreserveCombinedType(t *testing.T) {
	type received struct {
		method string
		path   string
		body   map[string]any
	}
	var got []received
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		item := received{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&item.body)
		}
		got = append(got, item)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}
	for _, name := range []string{"add_rule", "update_rule"} {
		props := toolProperties(t, s, name)
		for _, field := range []string{"side", "type", "match", "replace", "enabled"} {
			if _, ok := props[field]; !ok {
				t.Errorf("%s schema missing %s", name, field)
			}
		}
	}
	if props := toolProperties(t, s, "delete_rule"); props["id"] == nil {
		t.Error("delete_rule schema missing id")
	}

	if _, err := s.Call("add_rule", map[string]any{
		"side": "response", "type": "header", "match": "Server: .*", "replace": "Server: hidden", "enabled": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("add_rule", map[string]any{
		"type": "req-body", "match": "old", "replace": "new",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("update_rule", map[string]any{
		"id": 7, "side": "request", "type": "body", "match": "token=.*", "replace": "token=x", "enabled": false, "ord": 4,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("delete_rule", map[string]any{"id": 8}); err != nil {
		t.Fatal(err)
	}

	if got[0].body["type"] != "res-header" {
		t.Fatalf("side/type was not normalized for REST: %#v", got[0].body)
	}
	if got[1].body["type"] != "req-body" {
		t.Fatalf("legacy combined type was not preserved: %#v", got[1].body)
	}
	if got[2].method != http.MethodPut || got[2].path != "/api/rules/7" ||
		got[2].body["type"] != "req-body" || got[2].body["enabled"] != false || got[2].body["ord"] != float64(4) {
		t.Fatalf("update_rule REST call = %#v", got[2])
	}
	if got[3].method != http.MethodDelete || got[3].path != "/api/rules/8" {
		t.Fatalf("delete_rule REST call = %#v", got[3])
	}
}

func TestStartIntruderSchemaAndPayloadMirrorREST(t *testing.T) {
	var body map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/intruder/start" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"running":true}`)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}
	props := toolProperties(t, s, "start_intruder")
	for _, field := range []string{"repeat", "count", "delayMs", "grepMatch", "grepExtract", "processRules"} {
		if _, ok := props[field]; !ok {
			t.Errorf("start_intruder schema missing %s", field)
		}
	}

	_, err := s.Call("start_intruder", map[string]any{
		"target":       "https://example.com",
		"template":     "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"attackType":   "repeat",
		"payloads":     []any{},
		"count":        17,
		"threads":      8,
		"delayMs":      25,
		"grepMatch":    "success",
		"grepExtract":  `id=(\d+)`,
		"processRules": []any{"url-decode", "base64-decode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]any{
		"repeat":      float64(17),
		"threads":     float64(8),
		"delayMs":     float64(25),
		"grepMatch":   "success",
		"grepExtract": `id=(\d+)`,
	}
	for key, want := range wants {
		if body[key] != want {
			t.Errorf("%s = %#v, want %#v (body %#v)", key, body[key], want, body)
		}
	}
	rules, ok := body["processRules"].([]any)
	if !ok || len(rules) != 2 || rules[0] != "url-decode" {
		t.Fatalf("processRules = %#v", body["processRules"])
	}
}

func TestNewMutationToolResultsStayBounded(t *testing.T) {
	rows := make([]string, 250)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":%d}`, i)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[`+strings.Join(rows, ",")+`]}`)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}
	out, err := s.Call("forward_response", map[string]any{"id": 1})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Results   []json.RawMessage `json:"results"`
		Truncated bool              `json:"_truncated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bounded output is invalid JSON: %v", err)
	}
	if !got.Truncated || len(got.Results) != 200 {
		t.Fatalf("output was not bounded to 200 rows: truncated=%v rows=%d", got.Truncated, len(got.Results))
	}
}
