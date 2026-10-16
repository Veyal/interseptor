package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A request sent to the Repeater carries the captured Content-Length. Editing the
// body in the Repeater must not ship that stale count: too large and the origin
// waits for bytes that never arrive, too small and it truncates the payload, and
// on a keep-alive connection either one desynchronises the next request.
func TestRepeaterRecomputesStaleContentLength(t *testing.T) {
	type seen struct {
		header string
		body   string
	}
	got := make(chan seen, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{header: r.Header.Get("Content-Length"), body: string(b)}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// The captured request had a 5-byte body; the operator expanded it to 24.
	edited := `{"id":1043,"role":"admin"}`
	payload, _ := json.Marshal(map[string]string{
		"method":  "POST",
		"url":     upstream.URL + "/api/v1/orders",
		"headers": "Content-Type: application/json\nContent-Length: 5",
		"body":    edited,
	})
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("repeater send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("repeater send: %d %s", resp.StatusCode, b)
	}
	s := <-got
	if s.body != edited {
		t.Errorf("origin received a truncated or padded body: %q, want %q", s.body, edited)
	}
	if want := "26"; s.header != want {
		t.Errorf("origin saw Content-Length %q, want %q for the edited body", s.header, want)
	}
}
