package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthTimelineToolMirrorsREST(t *testing.T) {
	var method, uri string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, uri = r.Method, r.URL.RequestURI()
		io.WriteString(w, `{"steps":[]}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	toolProperties(t, s, "auth_timeline")
	if _, err := s.Call("auth_timeline", map[string]any{"flowId": 7, "windowSeconds": 30}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || uri != "/api/flows/7/auth-timeline?windowSeconds=30" {
		t.Fatalf("call = %s %s", method, uri)
	}
	if _, err := s.Call("auth_timeline", map[string]any{}); err == nil {
		t.Fatal("missing flowId must be rejected")
	}
}
