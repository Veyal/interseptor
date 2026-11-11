package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
)

// reviewRig is a plain-HTTP proxy in front of a recording upstream, exposing
// the Server so tests can inspect its internals.
type reviewRig struct {
	srv    *Server
	eng    *intercept.Engine
	client *http.Client
	seen   chan *http.Request
}

func newReviewRig(t *testing.T, handler http.HandlerFunc) *reviewRig {
	t.Helper()
	seen := make(chan *http.Request, 8)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		seen <- r
		handler(w, r)
	}))
	t.Cleanup(up.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := intercept.New()
	srv := New(st, capture.New(st), nil, eng, nil)
	upAddr := up.Listener.Addr().String()
	srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upAddr)
	}
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pl.Close() })
	go srv.Serve(pl)
	pu, _ := url.Parse("http://" + pl.Addr().String())
	return &reviewRig{
		srv:    srv,
		eng:    eng,
		client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}, Timeout: 10 * time.Second},
		seen:   seen,
	}
}

// A response buffered for match-&-replace reserves bytes from the shared
// transform budget. The reservation must be returned once the response has
// been written, or every transformed response permanently shrinks the budget
// until rules and the response hold silently stop applying.
func TestTransformedResponseReleasesBudget(t *testing.T) {
	body := strings.Repeat("a", 64<<10)
	rig := newReviewRig(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
	rig.srv.budget = newByteBudget(1 << 20)
	if err := rig.eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		resp, err := rig.client.Get("http://example.com/x")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(got) != len(body) {
			t.Fatalf("body len = %d, want %d", len(got), len(body))
		}
	}
	waitFor(t, "budget release", func() bool { return rig.srv.budget.used.Load() == 0 })
}

// Same contract on the raw MITM write path.
func TestTransformedResponseConnReleasesBudget(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x"}}); err != nil {
		t.Fatal(err)
	}
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.budget = newByteBudget(1 << 20)
	body := bytes.Repeat([]byte("b"), 64<<10)
	resp := &http.Response{
		StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Proto: "HTTP/1.1",
		Header:        http.Header{"Content-Type": {"text/plain"}},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewReader(body)),
		Request:       httptest.NewRequest(http.MethodGet, "https://example.com/x", nil),
	}
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		io.Copy(io.Discard, client)
	}()
	flow := srv.newFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/x"})
	if err := srv.writeResponseConn(server, resp, flow); err != nil {
		t.Fatalf("writeResponseConn: %v", err)
	}
	server.Close()
	if used := srv.budget.used.Load(); used != 0 {
		t.Fatalf("budget used after transformed response = %d, want 0", used)
	}
}

// A body-less POST (Content-Length: 0) must reach the origin with the same
// framing when a request body rule is enabled or the request was held and
// forwarded unedited. Replacing the empty body with a non-nil reader makes the
// transport treat the length as unknown and switch to chunked encoding.
func TestEmptyBodyPostKeepsContentLengthFraming(t *testing.T) {
	post := func(rig *reviewRig) <-chan error {
		errc := make(chan error, 1)
		go func() {
			req, _ := http.NewRequest(http.MethodPost, "http://example.com/submit", http.NoBody)
			resp, err := rig.client.Do(req)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			errc <- err
		}()
		return errc
	}
	check := func(t *testing.T, rig *reviewRig, errc <-chan error) {
		t.Helper()
		select {
		case r := <-rig.seen:
			if len(r.TransferEncoding) != 0 || r.ContentLength != 0 || r.Header.Get("Content-Length") != "0" {
				t.Fatalf("origin saw TransferEncoding=%v ContentLength=%d Content-Length=%q, want Content-Length: 0 and no chunking",
					r.TransferEncoding, r.ContentLength, r.Header.Get("Content-Length"))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("origin saw no request")
		}
		if err := <-errc; err != nil {
			t.Fatalf("client: %v", err)
		}
	}
	t.Run("request body rule", func(t *testing.T) {
		rig := newReviewRig(t, func(w http.ResponseWriter, r *http.Request) {})
		if err := rig.eng.SetRules([]store.Rule{{Enabled: true, Type: "req-body", Match: "zzz-nomatch", Replace: "x"}}); err != nil {
			t.Fatal(err)
		}
		check(t, rig, post(rig))
	})
	t.Run("held and forwarded unedited", func(t *testing.T) {
		rig := newReviewRig(t, func(w http.ResponseWriter, r *http.Request) {})
		rig.eng.SetEnabled(true)
		errc := post(rig)
		waitFor(t, "held request", func() bool { return len(rig.eng.Queue()) == 1 })
		if err := rig.eng.Forward(rig.eng.Queue()[0].ID, nil); err != nil {
			t.Fatal(err)
		}
		check(t, rig, errc)
	})
	t.Run("held and forwarded edited", func(t *testing.T) {
		rig := newReviewRig(t, func(w http.ResponseWriter, r *http.Request) {})
		rig.eng.SetEnabled(true)
		errc := post(rig)
		waitFor(t, "held request", func() bool { return len(rig.eng.Queue()) == 1 })
		raw := "POST /submit HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\nX-Edited: 1\r\n\r\n"
		if err := rig.eng.Forward(rig.eng.Queue()[0].ID, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		check(t, rig, errc)
	})
}

// One filtered request must count once in the suppressed report. persistable
// is consulted several times per exchange (request row, both body tees, the
// final record), and each call used to add another entry.
func TestOutOfScopeRequestIsCountedOnce(t *testing.T) {
	rig := newReviewRig(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	sc := scope.New()
	sc.SetRules([]store.ScopeRule{{Action: "include", Host: "app.example.com", Enabled: true}})
	rig.srv.Scope = sc
	rig.srv.SetCaptureScopeOnly(true)

	resp, err := rig.client.Post("http://other.example.com/screen", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	waitFor(t, "suppressed entry", func() bool { return rig.srv.RecentSuppressed(time.Minute).Total >= 1 })
	time.Sleep(50 * time.Millisecond) // let the handler finish its final record()
	rep := rig.srv.RecentSuppressed(time.Minute)
	if rep.Total != 1 || len(rep.Samples) != 1 {
		t.Fatalf("one filtered request counted %d times (%d samples), want 1", rep.Total, len(rep.Samples))
	}
}

// Samples are documented as the newest few drops. Once the ring has wrapped,
// the newest entry is no longer at the end of the backing slice.
func TestSuppressedSamplesAreNewestAfterRingWraps(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	total := suppressedRingSize + 40
	for i := 0; i < total; i++ {
		s.recordSuppressed(SuppressedRequest{Host: "h.example.com", Method: "GET", Path: fmt.Sprintf("/%d", i), Reason: SuppressedOutOfScope})
	}
	rep := s.RecentSuppressed(time.Hour)
	if len(rep.Samples) != suppressedSampleLimit {
		t.Fatalf("samples = %d, want %d", len(rep.Samples), suppressedSampleLimit)
	}
	for i, sm := range rep.Samples {
		if want := fmt.Sprintf("/%d", total-1-i); sm.Path != want {
			t.Fatalf("sample %d = %s, want %s (newest first)", i, sm.Path, want)
		}
	}
}
