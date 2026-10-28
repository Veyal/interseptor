package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// POST /api/session is a partial update, so the session tools must send only
// the fields they mean to change: set_session never touches unscoped (or
// hostHeaders unless given), and set_login_macro sends only loginMacro.
func TestSessionToolsSendOnlyTheirFields(t *testing.T) {
	var bodies []map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/session" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("set_session", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("set_session", map[string]any{"enabled": true, "headers": "Cookie: a=b", "hostHeaders": map[string]any{"example.com": "X: y"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call("set_login_macro", map[string]any{"enabled": true, "target": "https://example.com", "request": "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("bodies = %#v", bodies)
	}
	if len(bodies[0]) != 1 || bodies[0]["enabled"] != false {
		t.Fatalf("set_session enabled-only body = %#v", bodies[0])
	}
	if _, ok := bodies[1]["unscoped"]; ok || bodies[1]["headers"] != "Cookie: a=b" || bodies[1]["hostHeaders"] == nil {
		t.Fatalf("set_session body = %#v", bodies[1])
	}
	if _, ok := bodies[2]["loginMacro"]; !ok || len(bodies[2]) != 1 {
		t.Fatalf("set_login_macro body = %#v", bodies[2])
	}
}

func TestRunLoginMacroPointsAtSetLoginMacro(t *testing.T) {
	s := New("http://127.0.0.1:1")
	desc := s.tools["run_login_macro"].description
	if strings.Contains(desc, "set_session with loginMacro") || !strings.Contains(desc, "set_login_macro") {
		t.Fatalf("run_login_macro description = %q", desc)
	}
}
