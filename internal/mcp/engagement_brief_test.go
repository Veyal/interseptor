package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEngagementBriefTools(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody = nil
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		} else {
			io.Copy(io.Discard, r.Body)
		}
		io.WriteString(w, `{"version":3,"scope":"api.example.com"}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	out, err := s.Call("get_engagement_brief", map[string]any{})
	if err != nil || gotMethod != http.MethodGet || gotPath != "/api/engagement-brief" || !strings.Contains(out, `"version":3`) {
		t.Fatalf("get: %s %s out=%q err=%v", gotMethod, gotPath, out, err)
	}
	if _, err := s.Call("set_engagement_brief", map[string]any{"scope": "api.example.com", "doNotTouch": "billing"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut || gotBody["scope"] != "api.example.com" || gotBody["doNotTouch"] != "billing" {
		t.Fatalf("set: %s body=%v", gotMethod, gotBody)
	}
	if v, ok := gotBody["rateLimits"]; !ok || v != "" {
		t.Fatalf("unset fields must be sent empty, got %v", gotBody)
	}
}
