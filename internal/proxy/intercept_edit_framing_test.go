package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

type framingRig struct {
	eng    *intercept.Engine
	client *http.Client
	seen   chan seenRequest
}

type seenRequest struct {
	body          string
	contentLength string
	declared      int64
}

// newFramingRig starts an upstream that records what it receives, a proxy with
// an intercept engine in front of it, and a client that dials through the proxy.
func newFramingRig(t *testing.T, respBody string) *framingRig {
	t.Helper()
	seen := make(chan seenRequest, 4)
	upstream := http.NewServeMux()
	upstream.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- seenRequest{body: string(b), contentLength: r.Header.Get("Content-Length"), declared: r.ContentLength}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, respBody)
	})
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	us := &http.Server{Handler: upstream}
	go us.Serve(up)
	t.Cleanup(func() { us.Close() })

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := intercept.New()
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, up.Addr().String())
	}
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pl.Close() })
	go srv.Serve(pl)
	pu, _ := url.Parse("http://" + pl.Addr().String())
	return &framingRig{
		eng:    eng,
		client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}, Timeout: 10 * time.Second},
		seen:   seen,
	}
}

type clientResult struct {
	body          string
	contentLength string
	declared      int64
	err           error
}

func (r *framingRig) post(body string) <-chan clientResult {
	out := make(chan clientResult, 1)
	go func() {
		resp, err := r.client.Post("http://example.com/api", "application/json", strings.NewReader(body))
		if err != nil {
			out <- clientResult{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		out <- clientResult{body: string(b), contentLength: resp.Header.Get("Content-Length"), declared: resp.ContentLength, err: err}
	}()
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func recvClient(t *testing.T, c <-chan clientResult) clientResult {
	t.Helper()
	select {
	case r := <-c:
		if r.err != nil {
			t.Fatalf("client: %v", r.err)
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("client did not finish")
	}
	return clientResult{}
}

func recvSeen(t *testing.T, r *framingRig) seenRequest {
	t.Helper()
	select {
	case s := <-r.seen:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("upstream saw no request")
	}
	return seenRequest{}
}

// A body edited in the Pretty view grows well past the original and arrives as
// LF-only text with the stale Content-Length still in the header block. The
// target must receive the full edited body with a matching Content-Length.
func TestInterceptEditedRequestBodyReachesTargetWithCorrectContentLength(t *testing.T) {
	rig := newFramingRig(t, `{"ok":true}`)
	rig.eng.SetEnabled(true)
	orig := `{"a":1}`
	done := rig.post(orig)
	waitFor(t, "held request", func() bool { return len(rig.eng.Queue()) == 1 })

	edited := "{\n  \"a\": 1,\n  \"name\": \"edited in pretty view\"\n}"
	raw := "POST /api HTTP/1.1\nHost: example.com\nContent-Type: application/json\nContent-Length: " + strconv.Itoa(len(orig)) + "\n\n" + edited
	if err := rig.eng.Forward(rig.eng.Queue()[0].ID, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	got := recvSeen(t, rig)
	recvClient(t, done)
	if got.body != edited {
		t.Fatalf("target body = %q, want %q", got.body, edited)
	}
	if got.contentLength != strconv.Itoa(len(edited)) || got.declared != int64(len(edited)) {
		t.Fatalf("target Content-Length = %q (declared %d), want %d", got.contentLength, got.declared, len(edited))
	}
}

// Forwarding a held request with no edit (raw omitted) sends the original bytes.
func TestInterceptUneditedRequestForwardsOriginalBytes(t *testing.T) {
	rig := newFramingRig(t, `{"ok":true}`)
	rig.eng.SetEnabled(true)
	orig := "{\"a\":1,\r\n\"b\":2}"
	done := rig.post(orig)
	waitFor(t, "held request", func() bool { return len(rig.eng.Queue()) == 1 })
	if err := rig.eng.Forward(rig.eng.Queue()[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	got := recvSeen(t, rig)
	recvClient(t, done)
	if got.body != orig || got.contentLength != strconv.Itoa(len(orig)) {
		t.Fatalf("target got body %q / Content-Length %q, want original %q / %d", got.body, got.contentLength, orig, len(orig))
	}
}

// A held response edited in the Pretty view carries a stale Content-Length; the
// client must receive the full edited body with a matching Content-Length.
func TestInterceptEditedResponseBodyReachesClientWithCorrectContentLength(t *testing.T) {
	origBody := `{"ok":true}`
	rig := newFramingRig(t, origBody)
	rig.eng.SetResponseEnabled(true)
	done := rig.post(`{"a":1}`)
	waitFor(t, "held response", func() bool { return len(rig.eng.ResponseQueue()) == 1 })

	edited := "{\n  \"ok\": false,\n  \"note\": \"edited in pretty view\"\n}"
	raw := "HTTP/1.1 200 OK\nContent-Type: application/json\nContent-Length: " + strconv.Itoa(len(origBody)) + "\n\n" + edited
	if err := rig.eng.ForwardResponse(rig.eng.ResponseQueue()[0].ID, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	got := recvClient(t, done)
	if got.body != edited {
		t.Fatalf("client body = %q, want %q", got.body, edited)
	}
	if got.contentLength != strconv.Itoa(len(edited)) || got.declared != int64(len(edited)) {
		t.Fatalf("client Content-Length = %q (declared %d), want %d", got.contentLength, got.declared, len(edited))
	}
}

// Forwarding a held response with no edit (raw omitted) sends the original bytes.
func TestInterceptUneditedResponseForwardsOriginalBytes(t *testing.T) {
	origBody := "{\"ok\":true,\r\n\"n\":1}"
	rig := newFramingRig(t, origBody)
	rig.eng.SetResponseEnabled(true)
	done := rig.post(`{"a":1}`)
	waitFor(t, "held response", func() bool { return len(rig.eng.ResponseQueue()) == 1 })
	if err := rig.eng.ForwardResponse(rig.eng.ResponseQueue()[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	got := recvClient(t, done)
	if got.body != origBody || got.contentLength != strconv.Itoa(len(origBody)) {
		t.Fatalf("client got body %q / Content-Length %q, want original %q / %d", got.body, got.contentLength, origBody, len(origBody))
	}
}
