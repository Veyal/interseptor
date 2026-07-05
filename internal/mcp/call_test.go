package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCallInvokesToolInProcess verifies the exported Server.Call seam: a real
// registered tool is invoked by name in-process (no JSON-RPC round-trip), its
// arguments reach the control API, and its text result is returned to the caller.
func TestCallInvokesToolInProcess(t *testing.T) {
	var receivedTag string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/flows" {
			receivedTag = r.URL.Query().Get("tag")
			io.WriteString(w, `{"flows":[{"id":1,"host":"victim.test"}]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	s.report = func(Activity) {} // silence async activity POST

	out, err := s.Call("list_flows", map[string]any{"tag": "sqli", "limit": 10})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(out, "victim.test") {
		t.Fatalf("Call result missing control-backend payload: %q", out)
	}
	if receivedTag != "sqli" {
		t.Fatalf("Call did not forward arguments to the control API: tag=%q", receivedTag)
	}
}

// TestCallUnknownToolErrors verifies Call rejects an unregistered tool with the
// same "unknown tool" style the JSON-RPC dispatch uses.
func TestCallUnknownToolErrors(t *testing.T) {
	s := New("http://127.0.0.1:1") // control API unused; lookup fails first
	_, err := s.Call("no_such_tool", nil)
	if err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") || !strings.Contains(err.Error(), "no_such_tool") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

// TestCallReportsActivity verifies Call exercises the SAME activity-report path
// as JSON-RPC dispatch: one Activity per call, carrying tool name, summary,
// intent, outcome, and a result snippet — identical to callTool's behavior.
func TestCallReportsActivity(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/repeater/send" {
			io.WriteString(w, `{"id":7,"status":201,"method":"POST"}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer mock.Close()

	s := New(mock.URL)
	var got []Activity
	s.report = func(a Activity) { got = append(got, a) } // sync recorder (replaces async POST)

	if _, err := s.Call("send_request", map[string]any{
		"method": "POST",
		"url":    "https://victim.test/login",
		"intent": "probe login",
	}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 activity report, got %d", len(got))
	}
	a := got[0]
	if a.Tool != "send_request" || !a.OK {
		t.Fatalf("unexpected activity: %+v", a)
	}
	if a.Intent != "probe login" {
		t.Fatalf("activity intent not captured: %q", a.Intent)
	}
	if !strings.Contains(a.Summary, "method=POST") || !strings.Contains(a.Summary, "url=https://victim.test/login") {
		t.Fatalf("activity summary missing args: %q", a.Summary)
	}
	if a.Result == "" {
		t.Fatal("expected a result snippet in the activity report")
	}

	// A failing tool call (dead control plane) reports OK=false, same as JSON-RPC.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	s2 := New(dead.URL)
	var got2 []Activity
	s2.report = func(a Activity) { got2 = append(got2, a) }
	if _, err := s2.Call("list_flows", nil); err == nil {
		t.Fatal("expected an error from a dead control plane")
	}
	if len(got2) != 1 || got2[0].OK {
		t.Fatalf("errored Call should report exactly one OK=false activity: %+v", got2)
	}
}
