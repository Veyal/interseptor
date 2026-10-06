package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthzDifferentialToolMirrorsREST(t *testing.T) {
	var path string
	var body map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"kind":"authz_differential"}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	props := toolProperties(t, s, "authz_differential")
	for _, k := range []string{"flowId", "identities", "invalidBody", "sideEffectFlowId", "attachToFinding"} {
		if props[k] == nil {
			t.Errorf("schema missing %s", k)
		}
	}
	if _, err := s.Call("authz_differential", map[string]any{"flowId": 5, "invalidBody": "{", "attachToFinding": 2}); err != nil {
		t.Fatal(err)
	}
	if path != "/api/authz/differential" || body["flowId"] != float64(5) || body["invalidBody"] != "{" || body["attachToFinding"] != float64(2) {
		t.Fatalf("path=%s body=%v", path, body)
	}
}
