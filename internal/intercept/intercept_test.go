package intercept

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

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
	var n int
	e.SetNotifier(func() { n++ })
	req := newReq(t, "GET", "https://example.com/p", "")
	go func() { e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)
	_ = e.Forward(e.Queue()[0].ID, nil)
	time.Sleep(20 * time.Millisecond)
	if n < 2 {
		t.Fatalf("expected notifier to fire on hold and resolve, got %d", n)
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
