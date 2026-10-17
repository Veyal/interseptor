package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// aiScopeFixture serves the control API with scope limited to 127.0.0.1, plus
// an origin reachable both as 127.0.0.1 (in scope) and localhost (out of scope).
type aiScopeFixture struct {
	h      *Hub
	st     *store.Store
	api    *httptest.Server
	origin *httptest.Server
	port   string
}

func newAIScopeFixture(t *testing.T) *aiScopeFixture {
	t.Helper()
	h, st, _ := newHub(t)
	api := httptest.NewServer(h.Handler())
	t.Cleanup(api.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(origin.Close)
	rule := store.ScopeRule{Enabled: true, Action: "include", Host: "127.0.0.1"}
	if _, err := st.CreateScopeRule(&rule); err != nil {
		t.Fatal(err)
	}
	h.refreshScope()
	_, port, _ := strings.Cut(strings.TrimPrefix(origin.URL, "http://"), ":")
	return &aiScopeFixture{h: h, st: st, api: api, origin: origin, port: port}
}

func (f *aiScopeFixture) do(t *testing.T, method, path string, body any, ai bool) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.api.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if ai {
		req.Header.Set("X-Interseptor-Source", "ai")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *aiScopeFixture) flowCount(t *testing.T) int {
	t.Helper()
	flows, err := f.st.QueryFlowsFilter(store.FlowFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return len(flows)
}

func TestAISendsOutsideScopeAreRefused(t *testing.T) {
	f := newAIScopeFixture(t)
	outURL := "http://localhost:" + f.port + "/?oos=1"
	inURL := "http://127.0.0.1:" + f.port + "/"

	before := f.flowCount(t)
	code, body := f.do(t, "POST", "/api/repeater/send", map[string]any{"method": "GET", "url": outURL}, true)
	if code != http.StatusForbidden || !strings.Contains(body, `"code":"out_of_scope"`) || !strings.Contains(body, "request_human_input") {
		t.Fatalf("AI repeater out of scope = %d %s", code, body)
	}
	if f.flowCount(t) != before {
		t.Fatal("a refused AI send must not record or send anything")
	}
	if code, body := f.do(t, "POST", "/api/repeater/send", map[string]any{"method": "GET", "url": inURL}, true); code != http.StatusOK {
		t.Fatalf("AI repeater in scope = %d %s", code, body)
	}
	// The operator's own sends are unaffected by the AI guard.
	if code, body := f.do(t, "POST", "/api/repeater/send", map[string]any{"method": "GET", "url": outURL}, false); code != http.StatusOK {
		t.Fatalf("UI repeater out of scope = %d %s", code, body)
	}

	tmpl := "GET /?q=§a§ HTTP/1.1\r\nHost: localhost\r\n\r\n"
	code, body = f.do(t, "POST", "/api/intruder/start", map[string]any{
		"target": "http://localhost:" + f.port, "template": tmpl, "attackType": "sniper",
		"payloads": [][]string{{"x"}}, "threads": 1,
	}, true)
	if code != http.StatusForbidden || !strings.Contains(body, "out_of_scope") {
		t.Fatalf("AI intruder out of scope = %d %s", code, body)
	}

	code, body = f.do(t, "POST", "/api/ws/send", map[string]any{"url": "ws://localhost:" + f.port + "/ws", "message": "x"}, true)
	if code != http.StatusForbidden || !strings.Contains(body, "out_of_scope") {
		t.Fatalf("AI ws send out of scope = %d %s", code, body)
	}

	// Replaying a captured out-of-scope flow over the AI channel is refused too.
	seed := &store.Flow{Method: "GET", Scheme: "http", Host: "localhost", Port: atoiOr(f.port, 0), Path: "/", Status: 200}
	if _, err := f.st.InsertFlow(seed); err != nil {
		t.Fatal(err)
	}
	code, body = f.do(t, "POST", "/api/flows/"+itoa64(seed.ID)+"/replay", map[string]any{}, true)
	if code != http.StatusForbidden || !strings.Contains(body, "out_of_scope") {
		t.Fatalf("AI replay out of scope = %d %s", code, body)
	}
}

func TestAIIntruderThreadsAreCapped(t *testing.T) {
	f := newAIScopeFixture(t)
	tmpl := "GET /?q=§a§ HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"
	spec := map[string]any{
		"target": "http://127.0.0.1:" + f.port, "template": tmpl, "attackType": "sniper",
		"payloads": [][]string{{"x"}}, "threads": aiMaxIntruderThreads + 1,
	}
	code, body := f.do(t, "POST", "/api/intruder/start", spec, true)
	if code != http.StatusBadRequest || !strings.Contains(body, "at most 10") {
		t.Fatalf("AI intruder threads=%d = %d %s", aiMaxIntruderThreads+1, code, body)
	}
}

func TestAIChannelCannotWidenScope(t *testing.T) {
	f := newAIScopeFixture(t)
	rules := func() int {
		rs, err := f.st.ListScopeRules()
		if err != nil {
			t.Fatal(err)
		}
		return len(rs)
	}
	// A new include rule while an allow-list exists widens scope: refused.
	code, body := f.do(t, "POST", "/api/scope", map[string]any{"action": "include", "host": "evil.example.com", "enabled": true}, true)
	if code != http.StatusForbidden || !strings.Contains(body, "cannot widen") {
		t.Fatalf("AI widening include = %d %s", code, body)
	}
	// Repeating the existing include (scope_from_url twice) is idempotent.
	code, body = f.do(t, "POST", "/api/scope", map[string]any{"action": "include", "host": "127.0.0.1", "enabled": true}, true)
	if code != http.StatusOK || rules() != 1 {
		t.Fatalf("AI identical include = %d %s rules=%d", code, body, rules())
	}
	// Narrowing with an exclude rule is allowed.
	if code, body := f.do(t, "POST", "/api/scope", map[string]any{"action": "exclude", "host": "127.0.0.1", "path": "/admin", "enabled": true}, true); code != http.StatusCreated {
		t.Fatalf("AI exclude = %d %s", code, body)
	}
	// Updating or deleting can widen: refused for the AI channel.
	rs, _ := f.st.ListScopeRules()
	if code, _ := f.do(t, "DELETE", "/api/scope/"+itoa64(rs[0].ID), nil, true); code != http.StatusForbidden {
		t.Fatalf("AI delete = %d", code)
	}
	if code, _ := f.do(t, "PUT", "/api/scope/"+itoa64(rs[0].ID), map[string]any{"action": "include", "host": "*", "enabled": true}, true); code != http.StatusForbidden {
		t.Fatalf("AI update = %d", code)
	}
	// The operator can still widen scope.
	if code, body := f.do(t, "POST", "/api/scope", map[string]any{"action": "include", "host": "api.example.com", "enabled": true}, false); code != http.StatusCreated {
		t.Fatalf("UI include = %d %s", code, body)
	}
}

func TestAIFirstIncludeEstablishesScope(t *testing.T) {
	h, _, _ := newHub(t)
	api := httptest.NewServer(h.Handler())
	defer api.Close()
	f := &aiScopeFixture{h: h, api: api}
	if code, body := f.do(t, "POST", "/api/scope", map[string]any{"action": "include", "host": "app.example.com", "enabled": true}, true); code != http.StatusCreated {
		t.Fatalf("first AI include on an empty scope = %d %s", code, body)
	}
}

func TestTemplatePath(t *testing.T) {
	cases := map[string]string{
		"GET /api/§id§/x?q=§a§ HTTP/1.1\r\n":                              "/api/id/x",
		"POST http://example.com/login HTTP/1.1\r\nHost: example.com\r\n": "/login",
		"": "",
	}
	for in, want := range cases {
		if got := templatePath(in); got != want {
			t.Errorf("templatePath(%q) = %q, want %q", in, got, want)
		}
	}
}
