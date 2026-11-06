package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// mitmHoldRig is a proxy that MITMs one TLS origin and can hold responses for
// editing, plus a raw client tunnel that keeps HTTP/1.1 keep-alive semantics
// under the test's control (needed to observe framing and stream desync).
type mitmHoldRig struct {
	srv      *Server
	eng      *intercept.Engine
	addr     string
	ca       *tlsca.CA
	origin   *httptest.Server
	originHP string // host:port of the origin
}

func newMITMHoldRig(t *testing.T, h http.HandlerFunc) *mitmHoldRig {
	t.Helper()
	up := httptest.NewTLSServer(h)
	t.Cleanup(up.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("load CA: %v", err)
	}
	eng := intercept.New()
	srv := New(st, capture.New(st), ca, eng, nil)
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: originPool(up)}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)

	return &mitmHoldRig{
		srv:      srv,
		eng:      eng,
		addr:     ln.Addr().String(),
		ca:       ca,
		origin:   up,
		originHP: strings.TrimPrefix(up.URL, "https://"),
	}
}

// dial opens one CONNECT + TLS tunnel through the proxy.
func (r *mitmHoldRig) dial(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	raw, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", r.originHP, r.originHP)
	br := bufio.NewReader(raw)
	if cr, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"}); err != nil || cr.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %+v", err, cr)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(r.ca.CertPEM())
	host, _, _ := net.SplitHostPort(r.originHP)
	tc := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: host, NextProtos: []string{"http/1.1"}})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("client TLS handshake: %v", err)
	}
	return tc, bufio.NewReader(tc)
}

// waitHeld waits for the next held response and returns it.
func (r *mitmHoldRig) waitHeld(t *testing.T) intercept.HeldResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if q := r.eng.ResponseQueue(); len(q) > 0 {
			return q[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("response was never held")
	return intercept.HeldResponse{}
}

// A response edited from a body-carrying status to a bodyless one (204) must not
// put the buffered origin body on the wire: the MITM tunnel is keep-alive, so
// those bytes would be read as the head of the next response.
func TestMITMEditedBodylessResponseDoesNotDesyncKeepAlive(t *testing.T) {
	rig := newMITMHoldRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "hello")
	})
	rig.eng.SetResponseEnabled(true)
	tc, br := rig.dial(t)

	fmt.Fprintf(tc, "GET /first HTTP/1.1\r\nHost: %s\r\n\r\n", rig.originHP)
	held := rig.waitHeld(t)
	if err := rig.eng.ForwardResponse(held.ID, []byte("HTTP/1.1 204 No Content\n\nhello")); err != nil {
		t.Fatalf("forward edited response: %v", err)
	}

	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("204 carried a body %q", body)
	}

	// The next response on the same tunnel must still parse: a leaked body here
	// makes the client read "helloHTTP/1.1 ..." as a malformed status line.
	fmt.Fprintf(tc, "GET /second HTTP/1.1\r\nHost: %s\r\n\r\n", rig.originHP)
	held2 := rig.waitHeld(t)
	if err := rig.eng.ForwardResponse(held2.ID, nil); err != nil {
		t.Fatalf("forward second response: %v", err)
	}
	tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp2, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("second response desynced by leaked body bytes: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || string(body2) != "hello" {
		t.Fatalf("second response = %d %q, want 200 %q", resp2.StatusCode, body2, "hello")
	}
}

// A response edited from a bodyless status (204) to a body-carrying one (200)
// must carry framing, or the client waits forever for a body terminator that
// never arrives on the still-open tunnel.
func TestMITMEditedBodylessToBodystatusIsFramed(t *testing.T) {
	rig := newMITMHoldRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rig.eng.SetResponseEnabled(true)
	tc, br := rig.dial(t)

	fmt.Fprintf(tc, "GET /upgrade-status HTTP/1.1\r\nHost: %s\r\n\r\n", rig.originHP)
	held := rig.waitHeld(t)
	if !strings.Contains(string(held.Raw), "204") {
		t.Fatalf("held raw = %q, want the origin 204", held.Raw)
	}
	if err := rig.eng.ForwardResponse(held.ID, []byte("HTTP/1.1 200 OK\n\nedited-body")); err != nil {
		t.Fatalf("forward edited response: %v", err)
	}

	tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, rerr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if rerr != nil {
		t.Fatalf("read edited body (unframed response hangs): %v", rerr)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "edited-body" {
		t.Fatalf("response = %d %q, want 200 %q", resp.StatusCode, body, "edited-body")
	}
}

// A tunnelled client that sends "Expect: 100-continue" waits for an interim
// response before transmitting its body. The raw MITM loop owns the connection
// (net/http's server, which answers this, is not involved), so the proxy must
// answer it or the client stalls until its own timeout.
func TestMITMAnswersExpect100Continue(t *testing.T) {
	gotBody := make(chan string, 1)
	rig := newMITMHoldRig(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody <- string(b)
		io.WriteString(w, "ok")
	})
	tc, br := rig.dial(t)

	fmt.Fprintf(tc, "POST /upload HTTP/1.1\r\nHost: %s\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n", rig.originHP)
	tc.SetReadDeadline(time.Now().Add(2 * time.Second))
	interim, err := http.ReadResponse(br, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("no interim 100 Continue before the body (client would stall): %v", err)
	}
	if interim.StatusCode != http.StatusContinue {
		t.Fatalf("interim status = %d, want 100", interim.StatusCode)
	}
	tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(tc, "hello"); err != nil {
		t.Fatalf("write request body: %v", err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("read final response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("final response = %d %q, want 200 %q", resp.StatusCode, body, "ok")
	}
	select {
	case b := <-gotBody:
		if b != "hello" {
			t.Fatalf("origin received body %q, want %q", b, "hello")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("origin never received the request body")
	}
}

// Request trailers are part of the message body's framing: a chunked request
// that ends with trailer fields must reach the origin with those values, not
// with the empty placeholders the inbound request carries before its body is
// read (the outbound clone snapshots them too early).
func TestRequestTrailersReachOrigin(t *testing.T) {
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- fmt.Sprintf("body=%q trailer=%v", b, r.Trailer)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	srv := New(st, capture.New(st), nil, nil, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, upstream.URL+"/upload", pr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Trailer = http.Header{"X-Checksum": []string{"deadbeef"}}
	req.ContentLength = -1
	go func() {
		io.WriteString(pw, "payload")
		pw.Close()
	}()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	select {
	case got := <-seen:
		if !strings.Contains(got, "X-Checksum:[deadbeef]") {
			t.Fatalf("origin saw %s, want the trailer value preserved", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("origin saw no request")
	}
}

// A response too large to buffer for match-&-replace is forwarded untouched:
// maybeInterceptResponse restores the prefix it read into resp.Body. The raw
// MITM writer must forward *that* restored reader, not the original body it
// captured before the probe — otherwise the first (up to 2 MB) of the response
// is dropped while the origin's Content-Length still announces the whole body.
func TestMITMResponseBodyRuleForwardsWholeLargeBody(t *testing.T) {
	const size = 4 << 20
	payload := bytes.Repeat([]byte("A"), size)
	rig := newMITMHoldRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write(payload)
	})
	// A body rule that matches nothing still makes maybeInterceptResponse buffer
	// up to its 2 MB scan limit before restoring the stream.
	if err := rig.eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "zzz-no-match", Replace: "x"}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	tc, br := rig.dial(t)
	fmt.Fprintf(tc, "GET /big HTTP/1.1\r\nHost: %s\r\n\r\n", rig.originHP)
	tc.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response head: %v", err)
	}
	defer resp.Body.Close()
	if resp.ContentLength != size {
		t.Fatalf("Content-Length = %d, want %d", resp.ContentLength, size)
	}
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		t.Fatalf("read body after %d of %d bytes: %v", len(body), size, rerr)
	}
	if len(body) != size {
		t.Fatalf("forwarded body = %d bytes, want %d (prefix dropped)", len(body), size)
	}
	for i, b := range body {
		if b != 'A' {
			t.Fatalf("body[%d] = %q, want 'A'", i, b)
		}
	}
}
