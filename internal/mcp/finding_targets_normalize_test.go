package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPNormalizeFindingTargetsDefaultsToDryRun(t *testing.T) {
	var gotPath string
	var got map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"dryRun":true}`))
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	run := func(args string) {
		got = nil
		call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"normalize_finding_targets","arguments":` + args + `}}` + "\n"
		var out strings.Builder
		if err := s.Serve(strings.NewReader(call), &out); err != nil {
			t.Fatal(err)
		}
	}
	run(`{"id":7,"approve":[0,2]}`)
	if gotPath != "/api/findings/7/normalize-targets" || got["dryRun"] != true {
		t.Fatalf("default call: %s %+v", gotPath, got)
	}
	if a, _ := got["approve"].([]any); len(a) != 2 {
		t.Fatalf("approve lost: %+v", got)
	}
	run(`{"id":7,"approve":[0],"dryRun":false}`)
	if got["dryRun"] != false {
		t.Fatalf("explicit apply: %+v", got)
	}
}
