package intercept

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
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

func TestHoldContextCancelsAndRemovesRequest(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Decision, 1)
	go func() { got <- e.HoldContext(ctx, &store.Flow{}, newReq(t, "GET", "https://example.com/p", ""), nil) }()
	waitQueue(t, e, 1)
	cancel()
	select {
	case d := <-got:
		if !d.Drop || !d.Held {
			t.Fatalf("canceled hold decision = %+v, want held drop", d)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled hold did not return")
	}
	if len(e.Queue()) != 0 {
		t.Fatal("canceled request remained held")
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
		{Enabled: true, Type: "req-header", Match: `User-Agent: .*`, Replace: "User-Agent: interseptor"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	req := newReq(t, "GET", "https://example.com/", "")
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	if err := e.ApplyRules(req); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	if got := req.Header.Get("User-Agent"); got != "interseptor" {
		t.Fatalf("header rule not applied: %q", got)
	}
}

func TestApplyBodyRuleSkipsOversizedBodyWithoutReadingItAll(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{{
		ID: 9, Enabled: true, Type: "req-body", Match: `a`, Replace: "b",
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	const total = int64(ruleScanLimit) + 1<<20
	cr := &countReader{r: &byteNReader{left: total}}
	req := newReq(t, "POST", "https://example.com/submit", "")
	req.Body = io.NopCloser(cr)
	req.ContentLength = -1
	if err := e.ApplyRules(req); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	if cr.n > int64(ruleScanLimit)+1 {
		t.Fatalf("body rule consumed %d bytes, want at most %d", cr.n, int64(ruleScanLimit)+1)
	}
	rest, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read remainder: %v", err)
	}
	if int64(len(rest)) != total {
		t.Fatalf("forwarded %d bytes, want %d", len(rest), total)
	}
	if bytes.Contains(rest, []byte("b")) {
		t.Fatal("oversized body was rewritten")
	}
}

func TestApplyBodyRuleBigBodyOptInReadsPastScanLimit(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{{
		ID: 4, Enabled: true, Type: "req-body", Match: `a+`, Replace: "b", BigBody: true,
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	const total = int64(ruleScanLimit) + 32
	cr := &countReader{r: &byteNReader{left: total}}
	req := newReq(t, "POST", "https://example.com/submit", "")
	req.Body = io.NopCloser(cr)
	req.ContentLength = -1
	if err := e.ApplyRules(req); err != nil {
		t.Fatalf("ApplyRules: %v", err)
	}
	if cr.n != total {
		t.Fatalf("big_body rule read %d bytes, want %d", cr.n, total)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "b" {
		t.Fatalf("big_body rewrite = %q", body)
	}
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type byteNReader struct{ left int64 }

func (b *byteNReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	for i := range p {
		p[i] = 'a'
	}
	b.left -= int64(len(p))
	return len(p), nil
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

// changeLog collects Change events from the typed notifier in a thread-safe way;
// the notifier may fire concurrently.
type changeLog struct {
	mu sync.Mutex
	c  []Change
}

func (l *changeLog) record() func(Change) {
	return func(c Change) {
		l.mu.Lock()
		l.c = append(l.c, c)
		l.mu.Unlock()
	}
}

func (l *changeLog) snapshot() []Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Change(nil), l.c...)
}

func (l *changeLog) waitFor(t *testing.T, kind ChangeKind, id int64) Change {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, c := range l.snapshot() {
			if c.Kind == kind && c.ID == id {
				return c
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("change (kind=%d id=%d) never reported; have %+v", kind, id, l.snapshot())
	return Change{}
}

// The typed notifier must report a held request and its release with the right
// id, side and reason, and the generation must advance by one per change.
func TestChangeNotifierReportsHeldAndReleasedWithID(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	var log changeLog
	e.SetChangeNotifier(log.record())

	req := newReq(t, "GET", "https://example.com/p", "")
	go func() {
		e.Hold(&store.Flow{Host: "example.com", Path: "/p", Method: "GET"}, req, []byte("GET /p HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	}()
	waitQueue(t, e, 1)
	id := e.Queue()[0].ID

	held := log.waitFor(t, ChangeHeld, id)
	if held.Side != SideRequest {
		t.Fatalf("held side = %q, want %q", held.Side, SideRequest)
	}
	if held.Request.ID != id || held.Request.Flow == nil || held.Request.Flow.Path != "/p" {
		t.Fatalf("held change missing the affected item: %+v", held)
	}
	if held.Seq != e.Snapshot().Seq {
		t.Fatalf("held Seq %d != snapshot Seq %d", held.Seq, e.Snapshot().Seq)
	}

	if err := e.Forward(id, nil); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	rel := log.waitFor(t, ChangeReleased, id)
	if rel.Reason != ReleaseForwarded || rel.Side != SideRequest {
		t.Fatalf("release change = %+v, want forwarded/req", rel)
	}
	if rel.Seq != held.Seq+1 {
		t.Fatalf("release Seq %d must be held Seq %d + 1", rel.Seq, held.Seq)
	}
}

// Dropping must report ReleaseDropped (not forwarded) with the id.
func TestChangeNotifierReportsDropReason(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	var log changeLog
	e.SetChangeNotifier(log.record())
	req := newReq(t, "GET", "https://example.com/p", "")
	go func() { e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)
	id := e.Queue()[0].ID
	if err := e.Drop(id); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	rel := log.waitFor(t, ChangeReleased, id)
	if rel.Reason != ReleaseDropped {
		t.Fatalf("drop reason = %q, want %q", rel.Reason, ReleaseDropped)
	}
}

// A bulk toggle is a ChangeReset, not a stream of per-item releases: a consumer
// must re-snapshot rather than try to mirror a drain item by item.
func TestChangeNotifierResetOnToggleDrains(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	req := newReq(t, "GET", "https://example.com/p", "")
	go func() { e.Hold(&store.Flow{}, req, nil) }()
	waitQueue(t, e, 1)

	var log changeLog
	e.SetChangeNotifier(log.record())
	e.SetEnabled(false) // drains the queue
	// Give the drained Hold goroutine and the emit time to run.
	time.Sleep(20 * time.Millisecond)
	got := log.snapshot()
	if len(got) != 1 || got[0].Kind != ChangeReset {
		t.Fatalf("toggle-drain must emit exactly one ChangeReset, got %+v", got)
	}
	if len(e.Snapshot().Queue) != 0 {
		t.Fatal("queue should be empty after disabling")
	}
}

// The response side reports held/released with SideResponse and the id.
func TestResponseChangeNotifierReportsSideAndID(t *testing.T) {
	e := New()
	e.SetResponseEnabled(true)
	var log changeLog
	e.SetChangeNotifier(log.record())
	go func() { e.HoldResponse(&store.Flow{Host: "example.com"}, []byte("HTTP/1.1 200 OK\r\n\r\n")) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(e.ResponseQueue()) != 1 {
		t.Fatal("response never held")
	}
	id := e.ResponseQueue()[0].ID
	held := log.waitFor(t, ChangeHeld, id)
	if held.Side != SideResponse || held.Response.ID != id {
		t.Fatalf("response held change = %+v, want resp/id %d", held, id)
	}
	if err := e.DropResponse(id); err != nil {
		t.Fatalf("DropResponse: %v", err)
	}
	rel := log.waitFor(t, ChangeReleased, id)
	if rel.Side != SideResponse || rel.Reason != ReleaseDropped {
		t.Fatalf("response drop change = %+v", rel)
	}
}

// A late subscriber (one that reads Snapshot after changes happened) still sees
// the correct queue and a Seq that matches the number of changes.
func TestSnapshotIsAtomicAndCarriesSeq(t *testing.T) {
	e := New()
	e.SetEnabled(true) // seq 1
	r1 := newReq(t, "GET", "https://example.com/a", "")
	r2 := newReq(t, "GET", "https://example.com/b", "")
	go func() { e.Hold(&store.Flow{Host: "example.com", Path: "/a"}, r1, nil) }() // seq 2
	waitQueue(t, e, 1)
	go func() { e.Hold(&store.Flow{Host: "example.com", Path: "/b"}, r2, nil) }() // seq 3
	waitQueue(t, e, 2)

	s := e.Snapshot()
	if s.Seq != 3 {
		t.Fatalf("Seq = %d, want 3 (enable + 2 holds)", s.Seq)
	}
	if len(s.Queue) != 2 || s.Queue[0].Flow.Path != "/a" || s.Queue[1].Flow.Path != "/b" {
		t.Fatalf("snapshot queue not in arrival order: %+v", s.Queue)
	}
	if !s.Enabled {
		t.Fatal("snapshot must report enabled")
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
