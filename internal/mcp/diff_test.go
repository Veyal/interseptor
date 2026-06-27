package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// diff_flows must reach the control diff endpoint with both ids, return its text
// on success, and surface a clear tool error when a flow id is missing (404).
func TestDiffFlowsTool(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows/diff" {
			q := r.URL.Query()
			if q.Get("b") == "999" { // simulate a missing flow
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, "flow b not found")
				return
			}
			// Echo back the ids/format so the test can assert they were forwarded.
			io.WriteString(w, "DIFF flow "+q.Get("a")+" → flow "+q.Get("b")+
				"\nstatus 200→500; +12 bytes; 1 header change(s); 1 body line(s) changed [format="+q.Get("format")+"]")
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)

	call := func(args string) (text string, isErr bool) {
		raw := s.callTool(json.RawMessage(`{"name":"diff_flows","arguments":` + args + `}`))
		m := raw.(map[string]any)
		isErr, _ = m["isError"].(bool)
		content := m["content"].([]map[string]any)
		return content[0]["text"].(string), isErr
	}

	// Happy path: a/b forwarded, text returned.
	text, isErr := call(`{"a":1,"b":2}`)
	if isErr {
		t.Fatalf("diff_flows errored unexpectedly: %s", text)
	}
	if !strings.Contains(text, "DIFF flow 1 → flow 2") || !strings.Contains(text, "format=text") {
		t.Fatalf("diff result missing forwarded ids/format: %q", text)
	}

	// Aliases (id1/id2) are accepted.
	text, isErr = call(`{"id1":5,"id2":6}`)
	if isErr || !strings.Contains(text, "DIFF flow 5 → flow 6") {
		t.Fatalf("alias ids not accepted: %q (err=%v)", text, isErr)
	}

	// Missing id at the backend → tool error.
	text, isErr = call(`{"a":1,"b":999}`)
	if !isErr {
		t.Fatalf("expected a tool error for a missing flow, got: %q", text)
	}
	if !strings.Contains(text, "not found") {
		t.Fatalf("error should mention the missing flow: %q", text)
	}

	// A missing required arg → tool error before any request.
	text, isErr = call(`{"a":1}`)
	if !isErr || !strings.Contains(text, "b is required") {
		t.Fatalf("missing b should be a clear arg error, got: %q (err=%v)", text, isErr)
	}
}
