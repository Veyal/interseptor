package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// POST /api/session is a partial update: only the fields present change, so a
// caller saving just the login macro (MCP set_login_macro) cannot wipe the
// session headers, per-host headers or the unscoped opt-in.
func TestSetSessionIsAPartialUpdate(t *testing.T) {
	h, _, _ := newHub(t)
	post := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(body))
		rec := httptest.NewRecorder()
		(&sessionAPI{h}).setSession(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", body, rec.Code, rec.Body.String())
		}
	}
	get := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		(&sessionAPI{h}).getSession(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	post(`{"enabled":true,"headers":"Authorization: Bearer example","unscoped":true,"hostHeaders":{"example.com":"X-Test: one"}}`)
	post(`{"loginMacro":{"enabled":false,"target":"https://example.com","request":"GET /login HTTP/1.1\r\nHost: example.com\r\n\r\n"}}`)
	got := get()
	hh, _ := got["hostHeaders"].(map[string]any)
	if got["enabled"] != true || got["headers"] != "Authorization: Bearer example" || got["unscoped"] != true || hh["example.com"] != "X-Test: one" {
		t.Fatalf("a loginMacro-only POST changed the session: %#v", got)
	}
	if lm, _ := got["loginMacro"].(map[string]any); lm["target"] != "https://example.com" {
		t.Fatalf("loginMacro not saved: %#v", got["loginMacro"])
	}

	post(`{"enabled":false}`)
	got = get()
	if got["enabled"] != false || got["headers"] != "Authorization: Bearer example" || got["unscoped"] != true {
		t.Fatalf("an enabled-only POST changed other fields: %#v", got)
	}

	// Present fields still replace, including an explicit empty value.
	post(`{"headers":"","hostHeaders":{},"unscoped":false}`)
	got = get()
	hh, _ = got["hostHeaders"].(map[string]any)
	if got["headers"] != "" || len(hh) != 0 || got["unscoped"] != false {
		t.Fatalf("explicit empty values were not applied: %#v", got)
	}
}
