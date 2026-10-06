package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInterceptionSetupTools(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, nil
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		} else {
			io.Copy(io.Discard, r.Body)
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}

	if _, err := s.Call("get_interception_setup", map[string]any{}); err != nil || gotMethod != http.MethodGet || gotPath != "/api/interception-setup" {
		t.Fatalf("get: %s %s err=%v", gotMethod, gotPath, err)
	}

	_, err := s.Call("set_interception_setup", map[string]any{
		"proxyAddress": "127.0.0.1:8080",
		"enablers": []any{map[string]any{
			"tool": "frida", "scriptHash": "sha256:abc", "targetLibrary": "libflutter.so", "method": "hook",
		}},
	})
	if err != nil || gotMethod != http.MethodPut || gotPath != "/api/interception-setup" {
		t.Fatalf("set: %s %s err=%v", gotMethod, gotPath, err)
	}
	enablers, _ := gotBody["enablers"].([]any)
	if gotBody["proxyAddress"] != "127.0.0.1:8080" || len(enablers) != 1 {
		t.Fatalf("set body = %v", gotBody)
	}

	if _, err := s.Call("annotate_flow_interception", map[string]any{"id": 7, "annotation": "pinning_blocked"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/flows/7/interception" || gotBody["annotation"] != "pinning_blocked" {
		t.Fatalf("annotate: %s %s %v", gotMethod, gotPath, gotBody)
	}
	if _, err := s.Call("annotate_flow_interception", map[string]any{"id": 7, "annotation": "bogus"}); err == nil {
		t.Fatal("expected client-side rejection of unknown annotation")
	}
}
