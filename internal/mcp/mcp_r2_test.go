package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// r2Stub is a control plane that names a project and records every request, so
// a test can assert that a refused call never reached the destructive endpoint.
type r2Stub struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newR2Stub(t *testing.T, project string, projectStatus int) *r2Stub {
	t.Helper()
	st := &r2Stub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.calls = append(st.calls, r.Method+" "+r.URL.Path)
		st.mu.Unlock()
		if r.URL.Path == "/api/project" {
			if projectStatus != http.StatusOK {
				http.Error(w, "boom", projectStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"current": project, "dir": ""})
			return
		}
		if project != "" {
			w.Header().Set("X-Interseptor-Project", project)
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (st *r2Stub) wrote() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []string
	for _, c := range st.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	return out
}

// The four tools that replace or remove project data must honour the pin like
// the seven round 1 covered.
func TestR2ReplacingToolsHonourTheProjectPin(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"set_notes", map[string]any{"notes": "x"}},
		{"set_engagement_brief", map[string]any{"scope": "example.com"}},
		{"remove_authz_identity", map[string]any{"name": "admin"}},
		{"set_authz", map[string]any{"identities": []any{}, "mode": "replace"}},
		{"set_authz", map[string]any{"identities": []any{}, "mode": " Replace "}},
	}
	for _, tc := range cases {
		st := newR2Stub(t, "acme-prod", http.StatusOK)
		s := New(st.srv.URL)
		tc.args["expectedProject"] = "acme-staging"
		_, err := s.Call(tc.tool, tc.args)
		if err == nil || !strings.Contains(err.Error(), "acme-prod") {
			t.Errorf("%s pinned to another project: err = %v, want a rejection naming acme-prod", tc.tool, err)
		}
		if w := st.wrote(); len(w) != 0 {
			t.Errorf("%s reached the control API despite the wrong pin: %v", tc.tool, w)
		}
	}
}

// set_authz in the default merge mode destroys nothing, so the pin stays
// advisory there; the matching pin is accepted for every pinned tool.
func TestR2PinLeavesMergeAndMatchingCallsAlone(t *testing.T) {
	st := newR2Stub(t, "acme-prod", http.StatusOK)
	s := New(st.srv.URL)
	if _, err := s.Call("set_authz", map[string]any{"identities": []any{}, "expectedProject": "other"}); err != nil {
		t.Fatalf("merge-mode set_authz must not be pinned: %v", err)
	}
	if _, err := s.Call("set_notes", map[string]any{"notes": "x", "expectedProject": "ACME-prod"}); err != nil {
		t.Fatalf("matching pin rejected: %v", err)
	}
	if _, err := s.Call("set_notes", map[string]any{"notes": "x"}); err != nil {
		t.Fatalf("an unpinned call must keep working: %v", err)
	}
}

func TestR2PinnedToolSchemasAndInstructionsStayTrue(t *testing.T) {
	s := New("http://127.0.0.1:1")
	instr := mcpInstructions()
	for _, name := range []string{"set_authz", "remove_authz_identity", "set_notes", "set_engagement_brief"} {
		if !destructiveTools[name] {
			t.Errorf("%s is not in the pinned set", name)
		}
		_, schema, _ := s.ToolMeta(name)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["expectedProject"]; !ok {
			t.Errorf("%s: schema does not advertise expectedProject", name)
		}
	}
	for name := range destructiveTools {
		if !strings.Contains(sectionProjectPin, name) {
			t.Errorf("connect instructions do not name pinned tool %s", name)
		}
	}
	if !strings.Contains(instr, sectionProjectPin) {
		t.Fatal("instructions lost the PROJECT section")
	}
}

// A caller that supplied the pin asked for verification: when the active
// project cannot be determined the call must be refused, not run unverified.
func TestR2PinFailsClosedWhenTheProjectIsUnknown(t *testing.T) {
	for name, st := range map[string]*r2Stub{
		"project endpoint failing": newR2Stub(t, "acme-prod", http.StatusInternalServerError),
		"no project named":         newR2Stub(t, "", http.StatusOK),
	} {
		s := New(st.srv.URL)
		_, err := s.Call("prune_history", map[string]any{"hosts": "noise.example.com", "mode": "delete", "expectedProject": "acme-prod"})
		if err == nil || !strings.Contains(err.Error(), "expectedProject") {
			t.Errorf("%s: err = %v, want a refusal that names expectedProject", name, err)
		}
		if w := st.wrote(); len(w) != 0 {
			t.Errorf("%s: destructive call ran unverified: %v", name, w)
		}
		// Without the pin today's behaviour is kept.
		if _, err := s.Call("prune_history", map[string]any{"hosts": "noise.example.com", "mode": "delete"}); err != nil {
			t.Errorf("%s: unpinned call must still run: %v", name, err)
		}
	}
}

// A stale cached name is not a determination either: the project may have
// switched since that response.
func TestR2PinDoesNotTrustAStaleCache(t *testing.T) {
	st := newR2Stub(t, "acme-prod", http.StatusInternalServerError)
	s := New(st.srv.URL)
	s.noteProject("acme-prod", "")
	if _, err := s.Call("delete_rule", map[string]any{"id": 3, "expectedProject": "acme-prod"}); err == nil {
		t.Fatal("a pin verified only against a cached name must be refused")
	}
	if w := st.wrote(); len(w) != 0 {
		t.Fatalf("destructive call ran: %v", w)
	}
}

func TestR2ActivitySummaryCutsOnRuneBoundaries(t *testing.T) {
	// 59 ASCII bytes then a 3-byte rune: byte 60 falls inside it.
	in := strings.Repeat("a", 59) + strings.Repeat("語", 10)
	got := activitySummary("ws_send", map[string]any{"message": in})
	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("summary split a rune: %q", got)
	}
	if !strings.HasSuffix(got, "…") || !strings.Contains(got, "語") {
		t.Fatalf("summary should keep whole runes and mark the cut: %q", got)
	}
}

func TestR2ActivitySummaryMasksCredentials(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXZhbHVl"
	cases := map[string]map[string]any{
		"input":    {"op": "jwtdecode", "input": jwt},
		"message":  {"url": "wss://example.com/ws", "message": `{"password":"hunter2-s3cret"}`},
		"template": {"target": "https://example.com", "template": "GET / HTTP/1.1\r\nAuthorization: Bearer abcdef0123456789abcdef\r\n\r\n"},
		"source":   {"source": `API_KEY = "AIzaSyA1234567890abcdefghijklmnopqrstuvw"`},
		"url":      {"method": "GET", "url": "https://example.com/cb?access_token=Zm9vYmFyYmF6cXV4"},
	}
	secrets := []string{jwt, "hunter2", "abcdef0123456789abcdef", "AIzaSyA1234567890abcdefghijklmnopqrstuvw", "Zm9vYmFyYmF6cXV4"}
	for key, args := range cases {
		got := activitySummary("x", args)
		for _, sec := range secrets {
			if strings.Contains(got, sec[:7]) {
				t.Errorf("%s: summary echoes a credential: %q", key, got)
			}
		}
		if !strings.Contains(got, "redacted") {
			t.Errorf("%s: summary should carry a redaction placeholder: %q", key, got)
		}
	}
	// Ordinary arguments are untouched.
	if got := activitySummary("send_request", map[string]any{"method": "POST", "url": "https://example.com/login"}); got != "method=POST url=https://example.com/login" {
		t.Fatalf("plain summary changed: %q", got)
	}
}

func TestR2PromptsListMarksProjectPlaybooksUntrusted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"playbooks":[{"id":"idor","title":"IDOR","trusted":true,"source":"builtin"},{"id":"mine","title":"Mine","trusted":false,"source":"project","severityHint":"High"}]}`)
	}))
	defer srv.Close()
	s := New(srv.URL)
	desc := func(payload any, key string) map[string]string {
		b, _ := json.Marshal(payload)
		var d map[string][]map[string]any
		_ = json.Unmarshal(b, &d)
		out := map[string]string{}
		for _, it := range d[key] {
			out[strings.TrimPrefix(it["name"].(string), "playbook-")], _ = it["description"].(string)
		}
		return out
	}
	prompts, resources := desc(s.listPlaybookPrompts(), "prompts"), desc(s.listResources(), "resources")
	if !strings.Contains(prompts["mine"], "untrusted") {
		t.Errorf("prompts/list does not mark the project playbook untrusted: %q", prompts["mine"])
	}
	if !strings.Contains(prompts["mine"], "High") {
		t.Errorf("severity hint lost: %q", prompts["mine"])
	}
	if strings.Contains(prompts["idor"], "untrusted") {
		t.Errorf("bundled playbook marked untrusted: %q", prompts["idor"])
	}
	if strings.Contains(resources["mine"], "untrusted") != strings.Contains(prompts["mine"], "untrusted") {
		t.Errorf("resources/list and prompts/list disagree: %q vs %q", resources["mine"], prompts["mine"])
	}
}

// An id that is not a whole number in range must be a tool error, never id 0.
func TestR2IDArgumentsRejectUnusableNumbers(t *testing.T) {
	tools := map[string]string{
		"drop_request": "id", "forward_request": "id", "drop_response": "id", "forward_response": "id",
		"delete_rule": "id", "update_rule": "id", "list_ws_frames": "id", "prune_undo": "pruneId",
		"match_playbooks": "flowId", "auth_timeline": "flowId", "cross_host_token_replay": "flowId",
	}
	for tool, key := range tools {
		for _, bad := range []any{1e300, -1e300, 1.5, "1e300", "abc", "", 9.3e18} {
			st := newR2Stub(t, "p", http.StatusOK)
			s := New(st.srv.URL)
			_, err := s.Call(tool, map[string]any{key: bad})
			if err == nil {
				t.Errorf("%s %s=%v: accepted", tool, key, bad)
			}
			st.mu.Lock()
			calls := append([]string(nil), st.calls...)
			st.mu.Unlock()
			if len(calls) != 0 {
				t.Errorf("%s %s=%v reached the control API: %v", tool, key, bad, calls)
			}
		}
	}
}

// Optional integers declared in a schema are held to the same rule: a huge
// value is an error, not a silent default.
func TestR2SchemaIntegersMustFitAnInt(t *testing.T) {
	st := newR2Stub(t, "p", http.StatusOK)
	s := New(st.srv.URL)
	if _, err := s.Call("authz_run", map[string]any{"flowId": 1e300}); err == nil || !strings.Contains(err.Error(), "flowId") {
		t.Fatalf("authz_run flowId=1e300: err = %v", err)
	}
	if _, err := s.Call("list_authz_runs", map[string]any{"limit": float64(7)}); err != nil {
		t.Fatalf("a whole JSON number must still pass: %v", err)
	}
	if _, err := s.Call("list_authz_runs", map[string]any{"limit": "8"}); err != nil {
		t.Fatalf("a numeric string must still pass: %v", err)
	}
}

func r2RPC(t *testing.T, s *Server, body string) (status int, raw string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func r2ErrCode(t *testing.T, raw string) (code int, id string) {
	t.Helper()
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil || resp.Error == nil {
		t.Fatalf("not a JSON-RPC error response: %q (%v)", raw, err)
	}
	return resp.Error.Code, string(resp.ID)
}

func TestR2JSONRPCConformance(t *testing.T) {
	s := New("http://127.0.0.1:1")
	cases := []struct {
		name, body string
		code       int
		id         string
	}{
		{"empty batch", `[]`, -32600, "null"},
		{"not json", `{"jsonrpc":"2.0",`, -32700, "null"},
		{"number", `7`, -32600, "null"},
		{"string", `"ping"`, -32600, "null"},
		{"null", `null`, -32600, "null"},
		{"wrong jsonrpc", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, -32600, "1"},
		{"jsonrpc not a string", `{"jsonrpc":2.0,"id":1,"method":"ping"}`, -32600, "1"},
		{"jsonrpc null", `{"jsonrpc":null,"id":1,"method":"ping"}`, -32600, "1"},
		{"missing method", `{"jsonrpc":"2.0","id":"a"}`, -32600, `"a"`},
		{"method not a string", `{"jsonrpc":"2.0","id":1,"method":1}`, -32600, "1"},
		{"invalid without id", `{"jsonrpc":"2.0","method":1}`, -32600, "null"},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"nope"}`, -32601, "1"},
	}
	for _, tc := range cases {
		// HTTP transport.
		status, raw := r2RPC(t, s, tc.body)
		if status != http.StatusOK {
			t.Errorf("http %s: status %d, body %q", tc.name, status, raw)
			continue
		}
		if code, id := r2ErrCode(t, raw); code != tc.code || id != tc.id {
			t.Errorf("http %s: code %d id %s, want %d id %s", tc.name, code, id, tc.code, tc.id)
		}
		// stdio transport.
		var out bytes.Buffer
		if err := s.Serve(strings.NewReader(tc.body+"\n"), &out); err != nil {
			t.Fatalf("stdio %s: %v", tc.name, err)
		}
		if code, id := r2ErrCode(t, strings.TrimSpace(out.String())); code != tc.code || id != tc.id {
			t.Errorf("stdio %s: code %d id %s, want %d id %s", tc.name, code, id, tc.code, tc.id)
		}
	}
}

func TestR2JSONRPCBatchAndValidMessagesStillWork(t *testing.T) {
	s := New("http://127.0.0.1:1")
	// A batch mixing a request, a notification and two invalid members answers
	// the request and one Invalid Request per invalid member.
	body := `[{"jsonrpc":"2.0","id":9,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},1,{"foo":"bar"}]`
	check := func(transport, raw string) {
		var arr []struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) != 3 {
			t.Fatalf("%s: batch response = %q (%v), want 3 members", transport, raw, err)
		}
		if string(arr[0].ID) != "9" || arr[0].Error != nil {
			t.Errorf("%s: first member = %+v", transport, arr[0])
		}
		for _, m := range arr[1:] {
			if m.Error == nil || m.Error.Code != -32600 || string(m.ID) != "null" {
				t.Errorf("%s: invalid member answered %+v", transport, m)
			}
		}
	}
	_, raw := r2RPC(t, s, body)
	check("http", raw)
	var out bytes.Buffer
	_ = s.Serve(strings.NewReader(body+"\n"), &out)
	check("stdio", strings.TrimSpace(out.String()))

	// A batch of notifications only: 202 over HTTP, silence over stdio.
	status, raw := r2RPC(t, s, `[{"jsonrpc":"2.0","method":"notifications/initialized"}]`)
	if status != http.StatusAccepted || raw != "" {
		t.Errorf("notification batch: %d %q", status, raw)
	}
	out.Reset()
	_ = s.Serve(strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+`{"jsonrpc":"2.0","id":"x","method":"ping"}`+"\n"), &out)
	if got := strings.TrimSpace(out.String()); strings.Count(got, "\n") != 0 || !strings.Contains(got, `"id":"x"`) || !strings.Contains(got, `"result":{}`) {
		t.Errorf("stdio: notification then ping = %q", got)
	}
}

// The result line of a call lands in the same feed as the summary.
func TestR2ActivityResultMasksCredentials(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXZhbHVl"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"notes": "admin token: " + jwt})
	}))
	defer srv.Close()
	s := New(srv.URL)
	var got Activity
	s.SetActivityReporter(func(a Activity) { got = a })
	text, err := s.Call("get_notes", map[string]any{"intent": "check password=hunter2-s3cret"})
	if err != nil || !strings.Contains(text, jwt) {
		t.Fatalf("the tool result itself must stay intact: %v %q", err, text)
	}
	if strings.Contains(got.Result, jwt[:12]) || !strings.Contains(got.Result, "redacted") {
		t.Errorf("activity result echoes the credential: %q", got.Result)
	}
	if strings.Contains(got.Intent, "hunter2") {
		t.Errorf("activity intent echoes the credential: %q", got.Intent)
	}
}

func TestR2WholeNumberStringsAreIntegers(t *testing.T) {
	for in, want := range map[string]int{"7": 7, " 8 ": 8, "7.0": 7, "1e3": 1000} {
		if n, ok := intArg(in); !ok || n != want {
			t.Errorf("intArg(%q) = %d %v, want %d", in, n, ok, want)
		}
	}
	for _, bad := range []string{"1.5", "1e300", "abc", "", "NaN", "Inf"} {
		if n, ok := intArg(bad); ok {
			t.Errorf("intArg(%q) accepted as %d", bad, n)
		}
	}
}

// A request with no jsonrpc member keeps working exactly as before: existing
// hand-rolled clients and curl examples omit it. Only a member that is present
// and not the string "2.0" is refused (see TestR2JSONRPCConformance).
func TestR2AbsentJSONRPCMemberIsTolerated(t *testing.T) {
	s := New("http://127.0.0.1:1")
	const body = `{"id":1,"method":"ping"}`
	const want = `{"id":1,"jsonrpc":"2.0","result":{}}`
	if status, raw := r2RPC(t, s, body); status != http.StatusOK || raw != want {
		t.Errorf("http: %d %q, want %q", status, raw, want)
	}
	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(body+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != want {
		t.Errorf("stdio: %q, want %q", got, want)
	}
	// A notification and a batch member without it are handled too.
	if status, raw := r2RPC(t, s, `{"method":"notifications/initialized"}`); status != http.StatusAccepted || raw != "" {
		t.Errorf("notification without jsonrpc: %d %q", status, raw)
	}
	if _, raw := r2RPC(t, s, `[{"id":2,"method":"ping"}]`); raw != `[{"id":2,"jsonrpc":"2.0","result":{}}]` {
		t.Errorf("batch member without jsonrpc: %q", raw)
	}
}
