package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// An AI-originated send whose target has no readable host was allowed ("") and
// left to the send path. With an allow-list in place that is a scope decision
// made by nobody: it must be refused.
func TestAIScopeRefusalFailsClosedOnHostlessTarget(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "target.example.com"}); err != nil {
		t.Fatal(err)
	}
	h.refreshScope()

	for _, raw := range []string{"", "   ", "/just/a/path", "http:///nohost", "http:target.example.com/x", "x:@other.example.com/a", "http://[::1", "%zz"} {
		if msg := h.aiScopeRefusal(store.FlagAI, raw, ""); !strings.HasPrefix(msg, aiInvalidTargetPrefix) {
			t.Errorf("AI send to %q: refusal %q, want an invalid_target refusal", raw, msg)
		}
		// The operator's own sends are never scope-gated.
		if msg := h.aiScopeRefusal(0, raw, ""); msg != "" {
			t.Errorf("operator send to %q refused: %q", raw, msg)
		}
	}

	// Valid targets behave as before, for every shape the callers pass.
	allowed := []struct{ url, hint string }{
		{"https://target.example.com/a?b=1", ""},
		{"https://target.example.com", ""},             // session macro target without a path
		{"https://target.example.com", "/attack"},      // Intruder: path from the template
		{"wss://target.example.com/socket", ""},        // WebSocket send
		{"http://target.example.com:8080/x", ""},       // explicit port
		{"  https://TARGET.example.com/trimmed  ", ""}, // surrounding space, host case
	}
	for _, c := range allowed {
		if msg := h.aiScopeRefusal(store.FlagAI, c.url, c.hint); msg != "" {
			t.Errorf("in-scope AI send to %q refused: %q", c.url, msg)
		}
	}
	if msg := h.aiScopeRefusal(store.FlagAI, "https://other.example.net/", ""); !strings.HasPrefix(msg, "out_of_scope:") {
		t.Errorf("out-of-scope AI send: %q, want an out_of_scope refusal", msg)
	}
}

// The refusal reaches the agent as a structured 403 that names the real problem.
func TestRepeaterSendFromAIRefusesHostlessURL(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPost, "/api/repeater/send", strings.NewReader(`{"method":"GET","url":"http:target.example.com/x"}`))
	req.Header.Set("X-Interseptor-Source", "ai")
	rec := httptest.NewRecorder()
	(&toolsAPI{h}).repeaterSend(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusForbidden || out["code"] != "invalid_target" {
		t.Fatalf("AI Repeater send to a hostless URL: status %d body %s, want 403 invalid_target", rec.Code, rec.Body.String())
	}
}
