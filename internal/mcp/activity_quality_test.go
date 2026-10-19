package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// QA: a get_flow for a missing id was logged as Success with a 404 body.
func TestGetFlowMissingFlowIsAnError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"flow not found"}`, http.StatusNotFound)
	}))
	defer mock.Close()
	s := New(mock.URL)
	var got []Activity
	s.report = func(a Activity) { got = append(got, a) }
	for _, side := range []string{"both", "req", "res"} {
		got = nil
		if _, err := s.Call("get_flow", map[string]any{"id": float64(99999), "side": side}); err == nil {
			t.Fatalf("get_flow side=%s for a missing flow returned no error", side)
		}
		if len(got) != 1 || got[0].OK {
			t.Fatalf("side=%s activity = %+v, want one failed row", side, got)
		}
	}
}

// A response that is not there yet must not fail a get_flow whose request exists.
func TestGetFlowPendingResponseStillSucceeds(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("side") == "req" {
			io.WriteString(w, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
			return
		}
		http.Error(w, "no response", http.StatusNotFound)
	}))
	defer mock.Close()
	s := New(mock.URL)
	s.report = func(Activity) {}
	if _, err := s.Call("get_flow", map[string]any{"id": float64(5)}); err != nil {
		t.Fatalf("get_flow with a pending response: %v", err)
	}
}

// QA: Activity showed only "=== REQUEST ===" or "Summary:" as the result, and
// get_flow/send_request rows had no flow link.
func TestActivityResultIsInformativeAndLinksTheFlow(t *testing.T) {
	cases := []struct{ text, want string }{
		{"=== REQUEST ===\nGET /login HTTP/1.1\nHost: example.com\n\nUI: http://127.0.0.1:9966/#flow-42", "GET /login HTTP/1.1 · flow #42"},
		{"Summary:\n3 issues on example.com\n", "Summary: 3 issues on example.com"},
		{"Sent → flow #7 · 200\n\nUI: http://x/#flow-7", "Sent → flow #7 · 200"},
		{"plain line", "plain line"},
		{"", ""},
	}
	for _, c := range cases {
		if got := activityResult(c.text); got != c.want {
			t.Errorf("activityResult(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	long := strings.Repeat("a", 400) + "\n\nUI: http://x/#flow-9"
	if got := activityResult(long); !strings.HasSuffix(got, " · flow #9") || len(got) > 200 {
		t.Fatalf("a long result keeps its flow link within the cap: %q", got)
	}
}

// QA round 2: the send_request Activity row showed the raw flow-detail JSON as
// its result. It reads like the other rows: what was sent, the status and the
// flow link; the agent still gets the full JSON below the summary line.
func TestSendRequestActivityResultIsReadable(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":7,"method":"POST","scheme":"https","host":"example.com","port":443,"path":"/login","status":201,"durationMs":12,"error":""}`)
	}))
	defer mock.Close()
	s := New(mock.URL)
	var got []Activity
	s.report = func(a Activity) { got = append(got, a) }
	out, err := s.Call("send_request", map[string]any{"method": "POST", "url": "https://example.com/login"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":201`) {
		t.Fatalf("the agent lost the flow detail JSON:\n%s", out)
	}
	if len(got) != 1 {
		t.Fatalf("activity = %+v", got)
	}
	if want := "Sent → flow #7 · 201 · POST example.com/login · 12 ms"; got[0].Result != want {
		t.Fatalf("result = %q, want %q", got[0].Result, want)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":8,"method":"GET","scheme":"https","host":"example.com","port":443,"path":"/","status":0,"error":"dial tcp: connection refused"}`)
	}))
	defer failing.Close()
	s = New(failing.URL)
	got = nil
	s.report = func(a Activity) { got = append(got, a) }
	if _, err := s.Call("send_request", map[string]any{"url": "https://example.com/"}); err != nil {
		t.Fatal(err)
	}
	if want := "Sent → flow #8 · no response · GET example.com/ · dial tcp: connection refused"; got[0].Result != want {
		t.Fatalf("result = %q, want %q", got[0].Result, want)
	}
	if got[0].OK {
		t.Fatalf("a send_request that could not connect must report ok=false: %+v", got[0])
	}
}
