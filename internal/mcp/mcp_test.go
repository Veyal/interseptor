package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeResponses(t *testing.T, out string) []rpcResponse {
	t.Helper()
	var resps []rpcResponse
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		if r.JSONRPC != "2.0" {
			t.Fatalf("response missing jsonrpc 2.0: %q", line)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestActivitySummary(t *testing.T) {
	if got := activitySummary("send_request", map[string]any{"method": "POST", "url": "https://x/login"}); !strings.Contains(got, "method=POST") || !strings.Contains(got, "url=https://x/login") {
		t.Fatalf("send_request summary: %q", got)
	}
	if got := activitySummary("get_flow", map[string]any{"id": float64(42)}); got != "id=42" {
		t.Fatalf("get_flow summary: %q", got)
	}
	if got := activitySummary("active_scan", map[string]any{"target": "https://x"}); got != "target=https://x" {
		t.Fatalf("active_scan summary: %q", got)
	}
}

// Every tool call must be reported so the human can watch the AI work.
func TestActivityReporting(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/repeater/send" {
			io.WriteString(w, `{"id":7,"status":201,"method":"POST"}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	var got []Activity
	s.report = func(a Activity) { got = append(got, a) } // sync recorder (replaces async POST)

	s.callTool(json.RawMessage(`{"name":"send_request","arguments":{"method":"POST","url":"https://victim.test/login"}}`))

	if len(got) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(got))
	}
	a := got[0]
	if a.Tool != "send_request" || !a.OK {
		t.Fatalf("unexpected activity: %+v", a)
	}
	if !strings.Contains(a.Summary, "method=POST") || !strings.Contains(a.Summary, "url=https://victim.test/login") {
		t.Fatalf("summary missing args: %q", a.Summary)
	}
	if a.Result == "" {
		t.Fatal("expected a result snippet")
	}

	// A failing tool call is reported with OK=false (point at a dead control plane).
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	s2 := New(dead.URL)
	var got2 []Activity
	s2.report = func(a Activity) { got2 = append(got2, a) }
	s2.callTool(json.RawMessage(`{"name":"list_flows","arguments":{}}`))
	if len(got2) != 1 || got2[0].OK {
		t.Fatalf("errored call should report OK=false: %+v", got2)
	}
}

func TestMCPProtocolAndTools(t *testing.T) {
	var sentRepeater bool
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/flows":
			io.WriteString(w, `{"flows":[{"id":1,"method":"GET","host":"victim.test","path":"/login","status":200}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/repeater/send":
			sentRepeater = true
			io.WriteString(w, `{"id":7,"status":201,"method":"POST","host":"victim.test","path":"/login"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()

	srv := New(mock.URL)
	script := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_flows","arguments":{"host":"victim.test"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"send_request","arguments":{"method":"POST","url":"https://victim.test/login","body":"x=1"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := srv.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	// 4 requests carry an id (the notification produces no response).
	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, got %d: %s", len(resps), out.String())
	}

	// initialize
	var initRes struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ProtocolVersion == "" || initRes.ServerInfo.Name != "interceptor" || initRes.Capabilities.Tools == nil {
		t.Fatalf("bad initialize result: %s", resps[0].Result)
	}

	// tools/list contains the key tools
	var listRes struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(resps[1].Result, &listRes)
	names := map[string]bool{}
	for _, tl := range listRes.Tools {
		if tl.Description == "" || tl.InputSchema == nil {
			t.Fatalf("tool %q missing description/schema", tl.Name)
		}
		names[tl.Name] = true
	}
	for _, want := range []string{"list_flows", "get_flow", "send_request", "run_scanner", "set_intercept", "start_intruder"} {
		if !names[want] {
			t.Fatalf("tools/list missing %q; got %v", want, names)
		}
	}

	// tools/call list_flows → content text mentions the mock flow
	var callRes struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(resps[2].Result, &callRes)
	if callRes.IsError || len(callRes.Content) == 0 || !strings.Contains(callRes.Content[0].Text, "victim.test") {
		t.Fatalf("list_flows call result wrong: %s", resps[2].Result)
	}

	// tools/call send_request actually hit the repeater endpoint
	if !sentRepeater {
		t.Fatal("send_request did not call /api/repeater/send")
	}
}

func TestStreamableHTTPTransport(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows" {
			io.WriteString(w, `{"flows":[{"id":1,"host":"victim.test","path":"/login"}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()
	srv := New(mock.URL)

	post := func(jsonBody string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	// initialize → JSON-RPC result over application/json
	rec := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if rec.Code != 200 {
		t.Fatalf("initialize: code %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}
	resps := decodeResponses(t, rec.Body.String())
	if len(resps) != 1 || resps[0].Result == nil {
		t.Fatalf("bad initialize response: %s", rec.Body.String())
	}

	// tools/call list_flows → reaches the mock control backend
	rec = post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_flows","arguments":{}}}`)
	if !strings.Contains(rec.Body.String(), "victim.test") {
		t.Fatalf("tools/call did not reach control backend: %s", rec.Body.String())
	}

	// a notification (no id) → 202 Accepted, empty body
	rec = post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("notification should be 202 + empty, got %d %q", rec.Code, rec.Body.String())
	}

	// a batch with one request + one notification → array with a single response
	rec = post(`[{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":9,"method":"ping"}]`)
	var arr []rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("batch response not an array: %s", rec.Body.String())
	}
	if len(arr) != 1 || string(arr[0].ID) != "9" {
		t.Fatalf("batch should return 1 response for id 9: %s", rec.Body.String())
	}

	// GET → 405 (no server-initiated stream)
	greq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	grec := httptest.NewRecorder()
	srv.ServeHTTP(grec, greq)
	if grec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should be 405, got %d", grec.Code)
	}
}

func TestMCPUnknownToolIsToolError(t *testing.T) {
	srv := New("http://127.0.0.1:1") // unused
	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}` + "\n"
	var out bytes.Buffer
	srv.Serve(strings.NewReader(script), &out)
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(resps[0].Result, &res)
	if !res.IsError {
		t.Fatalf("unknown tool should be a tool error, got: %s", resps[0].Result)
	}
}

// boundJSON must keep large tool results VALID JSON (the old byte-truncate
// produced unparseable output exactly when results were big and interesting).
func TestBoundJSON(t *testing.T) {
	// Object with a long array field → capped in place, still valid JSON.
	rows := make([]string, 500)
	for i := range rows {
		rows[i] = `{"n":` + strconv.Itoa(i) + `}`
	}
	obj := `{"running":false,"results":[` + strings.Join(rows, ",") + `]}`
	out := boundJSON(obj, 200)
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("object branch produced invalid JSON: %v\n%s", err, out)
	}
	var results []json.RawMessage
	if err := json.Unmarshal(got["results"], &results); err != nil {
		t.Fatalf("results not an array: %v", err)
	}
	if len(results) != 200 {
		t.Fatalf("results not capped: got %d want 200", len(results))
	}
	if string(got["_truncated"]) != "true" || string(got["_total"]) != "500" || string(got["_shown"]) != "200" {
		t.Fatalf("missing/incorrect truncation markers: %s", out)
	}

	// A short object is returned byte-for-byte unchanged.
	small := `{"running":true,"results":[{"n":1}]}`
	if boundJSON(small, 200) != small {
		t.Fatalf("short object should be unchanged, got %s", boundJSON(small, 200))
	}

	// A bare top-level array → wrapped + capped, still valid JSON.
	arrRows := make([]string, 300)
	for i := range arrRows {
		arrRows[i] = `{"i":` + strconv.Itoa(i) + `}`
	}
	out = boundJSON(`[`+strings.Join(arrRows, ",")+`]`, 50)
	var wrap struct {
		Items     []json.RawMessage `json:"items"`
		Truncated bool              `json:"_truncated"`
		Shown     int               `json:"_shown"`
		Total     int               `json:"_total"`
	}
	if err := json.Unmarshal([]byte(out), &wrap); err != nil {
		t.Fatalf("array branch produced invalid JSON: %v\n%s", err, out)
	}
	if len(wrap.Items) != 50 || !wrap.Truncated || wrap.Shown != 50 || wrap.Total != 300 {
		t.Fatalf("array wrap wrong: items=%d trunc=%v shown=%d total=%d", len(wrap.Items), wrap.Truncated, wrap.Shown, wrap.Total)
	}

	// Non-JSON falls back to a bounded byte cap (still marked).
	noise := strings.Repeat("x", 100000)
	out = boundJSON(noise, 100)
	if len(out) >= len(noise) || !strings.Contains(out, "truncated") {
		t.Fatalf("non-JSON should be byte-truncated, got len=%d", len(out))
	}
}
