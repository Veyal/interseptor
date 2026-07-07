package intercept

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

func TestMatchField(t *testing.T) {
	f := &store.Flow{Host: "x.com", Path: "/admin/users", Method: "POST"}
	raw := []byte("POST /admin/users HTTP/1.1\r\nX-Role: admin\r\n\r\nuser=bob")
	if !matchField("url", regexp.MustCompile("admin"), f, raw) {
		t.Fatal("url should match /admin")
	}
	if !matchField("header", regexp.MustCompile("X-Role"), f, raw) {
		t.Fatal("header should match X-Role")
	}
	if !matchField("body", regexp.MustCompile("user="), f, raw) {
		t.Fatal("body should match user=")
	}
	if matchField("body", regexp.MustCompile("X-Role"), f, raw) {
		t.Fatal("X-Role is a header, not body")
	}
	if !matchField("method", regexp.MustCompile("POST"), f, raw) {
		t.Fatal("method should match POST")
	}
}

func TestInterceptFilterForwardsNonMatching(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	if err := e.SetInterceptFilter(true, "url", "admin"); err != nil {
		t.Fatalf("SetInterceptFilter: %v", err)
	}
	// A non-matching request must forward immediately (not be queued/blocked).
	req := newReq(t, "GET", "http://x.com/home", "")
	d := e.Hold(&store.Flow{Host: "x.com", Path: "/home"}, req, []byte("GET /home HTTP/1.1\r\nHost: x.com\r\n\r\n"))
	if d.Request != req {
		t.Fatal("non-matching request should be forwarded as-is")
	}
	if d.Held {
		t.Fatal("non-matching request must not be marked Held")
	}
	if len(e.Queue()) != 0 {
		t.Fatalf("non-matching request must not be held; queue=%d", len(e.Queue()))
	}
	// A bad regex is rejected.
	if err := e.SetInterceptFilter(true, "any", "(unterminated"); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func newReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return r
}

func TestDisabledPassesThrough(t *testing.T) {
	e := New()
	req := newReq(t, "GET", "https://example.com/p", "")
	d := e.Hold(&store.Flow{}, req, nil)
	if d.Drop || d.Request != req {
		t.Fatalf("disabled engine should pass the request straight through: %+v", d)
	}
	if len(e.Queue()) != 0 {
		t.Fatal("queue should be empty when disabled")
	}
}

func TestHoldThenForward(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://example.com/p", "")

	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{Host: "example.com"}, req, nil) }()

	waitQueue(t, e, 1)
	id := e.Queue()[0].ID
	if err := e.Forward(id, nil); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	d := recvDecision(t, got)
	if d.Drop || d.Request != req {
		t.Fatalf("expected forward of original request, got %+v", d)
	}
	if !d.Held {
		t.Fatal("a queued+forwarded request must report Held")
	}
	if len(e.Queue()) != 0 {
		t.Fatal("queue should drain after forward")
	}
}

func TestHoldThenDrop(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://example.com/p", "")

	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, req, nil) }()

	waitQueue(t, e, 1)
	if err := e.Drop(e.Queue()[0].ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if d := recvDecision(t, got); !d.Drop {
		t.Fatalf("expected drop decision, got %+v", d)
	}
}

func TestForwardWithEditedRaw(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://example.com/orig", "")

	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, req, nil) }()

	waitQueue(t, e, 1)
	raw := "GET /changed HTTP/1.1\r\nHost: example.com\r\nX-Edited: yes\r\n\r\n"
	if err := e.Forward(e.Queue()[0].ID, []byte(raw)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}
	d := recvDecision(t, got)
	if d.Drop || !d.Edited {
		t.Fatalf("expected edited forward, got %+v", d)
	}
	if d.Request.URL.Path != "/changed" || d.Request.URL.Scheme != "https" || d.Request.Header.Get("X-Edited") != "yes" {
		t.Fatalf("edited request not applied: %s %s %v", d.Request.URL.Scheme, d.Request.URL.Path, d.Request.Header)
	}
	// Host unedited: routing target (URL.Host) must still be the original host —
	// regression guard for the common (unedited) case.
	if d.Request.URL.Host != "example.com" {
		t.Fatalf("unedited Host should keep routing to the original host: URL.Host=%q", d.Request.URL.Host)
	}
	if d.Request.Host != "example.com" {
		t.Fatalf("unedited Host header mismatch: Host=%q", d.Request.Host)
	}
}

// Regression for the confused-deputy bug: parseEditedRequest previously always
// forced URL.Host back to the pre-edit original (origin-form parsing via
// http.ReadRequest can never populate URL.Host from the Host header text), so
// an operator editing the Host header in the intercept UI had the edit
// silently ignored for connection routing while the wire Host header still
// carried the edited value — connect to A, claim to be B. Editing Host must
// now retarget where the request is routed, exactly like a real proxy/client.
func TestForwardWithEditedHostRetargetsRouting(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://original.example.com/orig", "")

	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, req, nil) }()

	waitQueue(t, e, 1)
	raw := "GET /orig HTTP/1.1\r\nHost: retargeted.example.com:8443\r\n\r\n"
	if err := e.Forward(e.Queue()[0].ID, []byte(raw)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}
	d := recvDecision(t, got)
	if d.Drop || !d.Edited {
		t.Fatalf("expected edited forward, got %+v", d)
	}
	// The routing target (URL.Host, which net/http.Transport actually dials)
	// must follow the edited Host header, not the stale pre-edit original.
	if d.Request.URL.Host != "retargeted.example.com:8443" {
		t.Fatalf("edited Host must retarget URL.Host: got %q, want %q", d.Request.URL.Host, "retargeted.example.com:8443")
	}
	// The wire Host header and the routing target must always agree — no more
	// "connect to A, tell A the Host is B".
	if d.Request.Host != d.Request.URL.Host {
		t.Fatalf("wire Host header %q must match routing target URL.Host %q", d.Request.Host, d.Request.URL.Host)
	}
}

// Editing a held request's body should not require hand-fixing Content-Length:
// the forwarded request must carry the full edited body with a matching length.
// The UI textarea normalizes CRLF→LF, so exercise the LF-only form.
func TestForwardEditedBodyRecomputesContentLength(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "POST", "https://example.com/submit", "old")

	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)

	// Body grown well past the stale "Content-Length: 3"; LF line endings.
	want := "username=admin&password=hunter2"
	raw := "POST /submit HTTP/1.1\nHost: example.com\nContent-Length: 3\n\n" + want
	if err := e.Forward(e.Queue()[0].ID, []byte(raw)); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	d := recvDecision(t, got)
	if !d.Edited {
		t.Fatal("expected edited forward")
	}
	body, _ := io.ReadAll(d.Request.Body)
	if string(body) != want {
		t.Fatalf("body truncated by stale Content-Length: got %q want %q", body, want)
	}
	if d.Request.ContentLength != int64(len(want)) {
		t.Fatalf("ContentLength not recomputed: got %d want %d", d.Request.ContentLength, len(want))
	}
	if got := d.Request.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Fatalf("Content-Length header not updated: got %q want %d", got, len(want))
	}
}

func TestDisableDrainsQueue(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://example.com/p", "")
	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)

	e.SetEnabled(false) // should release everything held as forward
	if d := recvDecision(t, got); d.Drop {
		t.Fatalf("disabling should forward held requests, got drop")
	}
}

func TestApplyHeaderRule(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{
		{Enabled: true, Type: "req-header", Match: `User-Agent: .*`, Replace: "User-Agent: interceptor"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	req := newReq(t, "GET", "https://example.com/", "")
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	if err := e.ApplyRules(req); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	if got := req.Header.Get("User-Agent"); got != "interceptor" {
		t.Fatalf("header rule not applied: %q", got)
	}
}

func TestApplyBodyRule(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{
		{Enabled: true, Type: "req-body", Match: `secret`, Replace: "REDACTED"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	req := newReq(t, "POST", "https://example.com/", "password=secret&x=1")
	if err := e.ApplyRules(req); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "password=REDACTED&x=1" {
		t.Fatalf("body rule not applied: %q", body)
	}
	if req.ContentLength != int64(len("password=REDACTED&x=1")) {
		t.Fatalf("content-length not updated: %d", req.ContentLength)
	}
}

func TestSetRulesRejectsBadRegex(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{{Enabled: true, Type: "req-header", Match: "([", Replace: ""}}); err == nil {
		t.Fatal("expected bad regex to be rejected")
	}
}

func TestNotifierFires(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	var n atomic.Int32
	e.SetNotifier(func() { n.Add(1) }) // notifier may fire concurrently; must be thread-safe
	req := newReq(t, "GET", "https://example.com/p", "")
	go func() { e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)
	_ = e.Forward(e.Queue()[0].ID, nil)
	time.Sleep(20 * time.Millisecond)
	if n.Load() < 2 {
		t.Fatalf("expected notifier to fire on hold and resolve, got %d", n.Load())
	}
}

func waitQueue(t *testing.T, e *Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(e.Queue()) == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("queue never reached %d (have %d)", n, len(e.Queue()))
}

func recvDecision(t *testing.T, ch chan Decision) Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decision")
		return Decision{}
	}
}
