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

	"github.com/Veyal/interseptor/internal/version"
)

// TestCreateFindingWithImpact verifies that the create_finding MCP tool forwards
// the "impact" argument to the control-plane POST /api/findings endpoint (not "fix"),
// and that update_finding likewise forwards "impact".
func TestCreateFindingWithImpact(t *testing.T) {
	var createBody, updateBody map[string]any

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings":
			json.NewDecoder(r.Body).Decode(&createBody)
			io.WriteString(w, `{"id":1,"title":"test","impact":"attacker reads PII","severity":"High","status":"open","flows":[],"blocks":[]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/findings/1":
			json.NewDecoder(r.Body).Decode(&updateBody)
			io.WriteString(w, `{"id":1,"title":"test","impact":"full account takeover","severity":"High","status":"verified","flows":[],"blocks":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {} // silence activity reporting

	script := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"test","severity":"High","impact":"attacker reads PII"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_finding","arguments":{"id":1,"status":"verified","impact":"full account takeover"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// create_finding must have forwarded "impact", not "fix".
	if createBody["impact"] != "attacker reads PII" {
		t.Fatalf("create_finding: expected impact=%q in body, got %v", "attacker reads PII", createBody)
	}
	if _, hasFix := createBody["fix"]; hasFix {
		t.Fatalf("create_finding: must not forward 'fix' when impact is set, body=%v", createBody)
	}

	// update_finding must have forwarded "impact".
	if updateBody["impact"] != "full account takeover" {
		t.Fatalf("update_finding: expected impact=%q in body, got %v", "full account takeover", updateBody)
	}
}

// TestCreateUpdateFindingUIURL verifies that create_finding and update_finding
// MCP tool responses include the /#finding-<id> deep-link URL.
func TestCreateUpdateFindingUIURL(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings":
			io.WriteString(w, `{"id":42,"title":"test","severity":"High","status":"open","flows":[],"blocks":[]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/findings/42":
			io.WriteString(w, `{"id":42,"title":"test","severity":"High","status":"verified","flows":[],"blocks":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_finding","arguments":{"title":"test","severity":"High"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_finding","arguments":{"id":42,"status":"verified"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	resps := decodeResponses(t, out.String())
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}

	extract := func(res json.RawMessage) string {
		var r struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
		}
		json.Unmarshal(res, &r)
		if len(r.Content) == 0 {
			return ""
		}
		return r.Content[0].Text
	}

	createText := extract(resps[0].Result)
	if !strings.Contains(createText, "/#finding-42") {
		t.Fatalf("create_finding response missing /#finding-42 URL:\n%s", createText)
	}
	updateText := extract(resps[1].Result)
	if !strings.Contains(updateText, "/#finding-42") {
		t.Fatalf("update_finding response missing /#finding-42 URL:\n%s", updateText)
	}
}

// TestSendRequestGetFlowUIURL verifies send_request and get_flow append /#flow-<id>.
func TestSendRequestGetFlowUIURL(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/repeater/send":
			io.WriteString(w, `{"id":6010,"status":200,"method":"GET"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/flows/6010/raw"):
			io.WriteString(w, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	sendOut, err := s.Call("send_request", map[string]any{"url": "https://example.com/"})
	if err != nil {
		t.Fatalf("send_request: %v", err)
	}
	if !strings.Contains(sendOut, "/#flow-6010") {
		t.Fatalf("send_request missing flow UI URL:\n%s", sendOut)
	}

	getOut, err := s.Call("get_flow", map[string]any{"id": float64(6010), "side": "req"})
	if err != nil {
		t.Fatalf("get_flow: %v", err)
	}
	if !strings.Contains(getOut, "/#flow-6010") {
		t.Fatalf("get_flow missing flow UI URL:\n%s", getOut)
	}
}

// TestUpdateFindingBodyParam verifies that create_finding and update_finding
// forward the "body" argument to the REST API, and that update_finding also
// forwards "cvss".
func TestUpdateFindingBodyParam(t *testing.T) {
	var createBody, updateBody map[string]any

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/findings":
			json.NewDecoder(r.Body).Decode(&createBody)
			io.WriteString(w, `{"id":5,"title":"t","severity":"High","status":"open","flows":[],"blocks":[]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/findings/5":
			json.NewDecoder(r.Body).Decode(&updateBody)
			io.WriteString(w, `{"id":5,"title":"t","severity":"High","status":"open","flows":[],"blocks":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	bodyJSON := `[{"type":"text","md":"hello"}]`

	// Build valid JSON-RPC messages programmatically to avoid manual escaping.
	buildCall := func(id int, name string, args map[string]any) string {
		msg := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params":  map[string]any{"name": name, "arguments": args},
		}
		b, _ := json.Marshal(msg)
		return string(b)
	}
	createLine := buildCall(1, "create_finding", map[string]any{"title": "t", "severity": "High", "body": bodyJSON, "cvss": "7.5"})
	updateLine := buildCall(2, "update_finding", map[string]any{"id": 5, "body": bodyJSON, "cvss": "9.8"})

	script := createLine + "\n" + updateLine + "\n"

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if createBody["body"] != bodyJSON {
		t.Fatalf("create_finding: body not forwarded, got %v", createBody)
	}
	if createBody["cvss"] != "7.5" {
		t.Fatalf("create_finding: cvss not forwarded, got %v", createBody)
	}
	if updateBody["body"] != bodyJSON {
		t.Fatalf("update_finding: body not forwarded, got %v", updateBody)
	}
	if updateBody["cvss"] != "9.8" {
		t.Fatalf("update_finding: cvss not forwarded, got %v", updateBody)
	}
}

// TestListFlowsMCPTagFilter verifies that the list_flows MCP tool forwards the
// "tag" argument as a ?tag= query param to GET /api/flows.
func TestListFlowsMCPTagFilter(t *testing.T) {
	var receivedTag string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows" {
			receivedTag = r.URL.Query().Get("tag")
			io.WriteString(w, `{"flows":[]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_flows","arguments":{"tag":"sqli","limit":10}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if receivedTag != "sqli" {
		t.Fatalf("expected tag=sqli forwarded, got %q", receivedTag)
	}
}

// TestAddFindingPocPosition verifies that add_finding_poc forwards the "position"
// arg to POST /api/findings/:id/flows.
func TestAddFindingPocPosition(t *testing.T) {
	var receivedBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/flows") {
			json.NewDecoder(r.Body).Decode(&receivedBody)
			io.WriteString(w, `{"id":1,"title":"t","severity":"High","status":"open","flows":[],"blocks":[]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_finding_poc","arguments":{"findingId":1,"flowId":7,"note":"poc","position":2}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if pos, ok := receivedBody["position"]; !ok || pos != float64(2) {
		t.Fatalf("position not forwarded: body=%v", receivedBody)
	}
}

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
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ProtocolVersion == "" || initRes.ServerInfo.Name != "interseptor" || initRes.Capabilities.Tools == nil {
		t.Fatalf("bad initialize result: %s", resps[0].Result)
	}
	// serverInfo.version must report the resolved running version
	// (version.String(), which honors `go install .../interseptor@vX.Y.Z`
	// build info), not the stale baked-in version.Version placeholder.
	if initRes.ServerInfo.Version != version.String() {
		t.Fatalf("serverInfo.version = %q, want version.String() = %q", initRes.ServerInfo.Version, version.String())
	}
	// Agents must be told where to report bugs/gaps in Interseptor itself.
	if !strings.Contains(initRes.Instructions, "github.com/Veyal/interseptor/issues") {
		t.Fatalf("initialize instructions missing the issues URL: %q", initRes.Instructions)
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

// TestListFlowsMCPDefaultsIncludeTools verifies list_flows always asks the
// control API for tool traffic (includeTools=1) so agents see active-scan /
// repeater rows that History hides by default.
func TestListFlowsMCPDefaultsIncludeTools(t *testing.T) {
	var gotInclude string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows" {
			gotInclude = r.URL.Query().Get("includeTools")
			io.WriteString(w, `{"flows":[],"truncated":false}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_flows","arguments":{"limit":5}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotInclude != "1" {
		t.Fatalf("list_flows should default includeTools=1, got %q", gotInclude)
	}
}

// TestListFlowsMCPIncludeToolsFalse allows agents to opt into History-shaped
// filtering by passing includeTools:false.
func TestListFlowsMCPIncludeToolsFalse(t *testing.T) {
	var gotInclude string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows" {
			gotInclude = r.URL.Query().Get("includeTools")
			io.WriteString(w, `{"flows":[]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_flows","arguments":{"includeTools":false}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotInclude != "" && gotInclude != "0" {
		t.Fatalf("includeTools:false should omit or set 0, got %q", gotInclude)
	}
}

// TestAddFindingImageForwardsToREST verifies add_finding_image posts to
// /api/findings/{id}/images with data/caption/position.
func TestAddFindingImageForwardsToREST(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/images") {
			gotPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&gotBody)
			io.WriteString(w, `{"id":7,"title":"t","blocks":[{"type":"image","hash":"aa","caption":"cap"}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_finding_image","arguments":{"findingId":7,"data":"AAAA","mime":"image/png","caption":"cap","position":2}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotPath != "/api/findings/7/images" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["data"] != "AAAA" || gotBody["caption"] != "cap" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["position"] != float64(2) {
		t.Fatalf("position = %v", gotBody["position"])
	}
}

// TestRenderFlowPreviewAttachForwardsToREST verifies render_flow_preview with
// findingId posts to /api/findings/{id}/flow-preview.
func TestRenderFlowPreviewAttachForwardsToREST(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/flow-preview") {
			gotPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&gotBody)
			io.WriteString(w, `{"id":3,"title":"t","blocks":[{"type":"image","hash":"bb","caption":"cap"}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {}

	script := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"render_flow_preview","arguments":{"flowId":9,"findingId":3,"side":"req","pretty":true,"layout":"horizontal","theme":"light","caption":"cap","position":1}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(script), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if gotPath != "/api/findings/3/flow-preview" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["flowId"] != float64(9) || gotBody["side"] != "req" || gotBody["caption"] != "cap" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["pretty"] != true || gotBody["layout"] != "horizontal" || gotBody["theme"] != "light" {
		t.Fatalf("options not forwarded: %+v", gotBody)
	}
}

// TestHumanInputScopeBoundary verifies the narrowing required by issue #7:
// request_human_input is for Interseptor/target-engagement decisions only,
// NOT for host/OS/dev work (sudo, package installs, SSH/Tailscale setup,
// coding/git questions, etc.). Both the connect instructions and the tool
// description must make this boundary explicit.
func TestHumanInputScopeBoundary(t *testing.T) {
	srv := New("http://127.0.0.1:0")

	instr := mcpInstructions()
	for _, want := range []string{"request_human_input", "engagement", "operator"} {
		if !strings.Contains(instr, want) {
			t.Fatalf("instructions missing %q (got: ...%s...)", want, excerpt(instr, want))
		}
	}
	for _, want := range []string{"sudo", "Tailscale", "Cursor", "Remote Login"} {
		if !strings.Contains(instr, want) {
			t.Fatalf("instructions missing off-limits example %q (got: ...%s...)", want, excerpt(instr, want))
		}
	}
	if !strings.Contains(instr, "Do NOT route") && !strings.Contains(instr, "do not route") {
		t.Fatalf("instructions missing the do-not-route-through-request_human_input note")
	}

	desc := srv.tools["request_human_input"].description
	if desc == "" {
		t.Fatal("request_human_input tool not registered")
	}
	if !strings.Contains(desc, "Use for") || !strings.Contains(desc, "Do not use for") {
		t.Fatalf("request_human_input description missing Use-for/Do-not-use-for sections: %q", desc)
	}
	for _, want := range []string{"scope", "destructive", "auth", "authority"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("request_human_input description missing Use-for item %q: %q", want, desc)
		}
	}
	for _, want := range []string{"sudo", "Tailscale", "Cursor", "package install"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("request_human_input description missing Do-not-use-for item %q: %q", want, desc)
		}
	}
	if _, ok := srv.tools["get_human_response"]; !ok {
		t.Fatal("get_human_response tool was removed")
	}
}

// excerpt returns a short window of s around the first occurrence of needle,
// or the tail of s if needle is absent — for readable failure messages.
func excerpt(s, needle string) string {
	i := strings.Index(s, needle)
	if i < 0 {
		if len(s) > 200 {
			return s[len(s)-200:]
		}
		return s
	}
	start := i - 60
	if start < 0 {
		start = 0
	}
	end := i + len(needle) + 60
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
