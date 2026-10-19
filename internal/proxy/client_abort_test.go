package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// A client that hangs up while its (non-held) request is still in flight is a
// client abort, not an upstream 502: the flow must record no status and the
// terminal error "client disconnected", never "502 / upstream: context canceled".
func TestClientAbortInFlightIsRecordedDistinctly(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, &recEvents{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/slow", nil)
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("expected the aborted request to fail")
	}

	flows := waitFlows(t, s, 1)
	f := flows[0]
	if f.Status != 0 {
		t.Fatalf("status = %d, want 0 (no response was produced)", f.Status)
	}
	if f.Error != errClientAborted {
		t.Fatalf("error = %q, want %q", f.Error, errClientAborted)
	}
}

// mitmAbortHarness starts a TLS origin handler behind a MITM proxy and returns
// a raw-CONNECT dialer that yields a TLS (HTTP/1.1-only) client conn.
type mitmAbortHarness struct {
	t      *testing.T
	st     *store.Store
	origin *httptest.Server
	ln     net.Listener
	pool   *x509.CertPool
}

func newMITMAbortHarness(t *testing.T, h http.HandlerFunc) *mitmAbortHarness {
	t.Helper()
	origin := httptest.NewTLSServer(h)
	t.Cleanup(origin.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	srv := New(st, capture.New(st), ca, nil, &recEvents{})
	up := x509.NewCertPool()
	up.AddCert(origin.Certificate())
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: up}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	return &mitmAbortHarness{t: t, st: st, origin: origin, ln: ln, pool: pool}
}

// dial opens CONNECT through the proxy and completes an HTTP/1.1-only TLS handshake.
func (h *mitmAbortHarness) dial() net.Conn {
	h.t.Helper()
	c, err := net.Dial("tcp", h.ln.Addr().String())
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	target := strings.TrimPrefix(h.origin.URL, "https://")
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		h.t.Fatalf("CONNECT: %v %v", resp, err)
	}
	host, _, _ := net.SplitHostPort(target)
	tc := tls.Client(c, &tls.Config{RootCAs: h.pool, ServerName: host, NextProtos: []string{"http/1.1"}})
	if err := tc.Handshake(); err != nil {
		h.t.Fatalf("handshake: %v", err)
	}
	return tc
}

// An HTTP/1.1 client over CONNECT that disconnects mid-request must be recorded
// as a client abort, not a normal 200 lasting the full upstream time.
func TestMITMHTTP1ClientDisconnectIsRecordedAsAbort(t *testing.T) {
	release := make(chan struct{})
	h := newMITMAbortHarness(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		io.WriteString(w, "late")
	})
	defer close(release)
	c := h.dial()
	fmt.Fprintf(c, "GET /slow/x HTTP/1.1\r\nHost: %s\r\n\r\n", strings.TrimPrefix(h.origin.URL, "https://"))
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	c.Close()
	f := waitFlows(t, h.st, 1)[0]
	if f.Status != 0 || f.Error != errClientAborted {
		t.Fatalf("flow = status %d error %q, want 0 / %q", f.Status, f.Error, errClientAborted)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("abort took %v, expected prompt", time.Since(start))
	}
}

// Keep-alive reuse, request bodies, and a slow-but-alive client must be unaffected.
func TestMITMHTTP1KeepAliveBodyAndSlowClientUnaffected(t *testing.T) {
	h := newMITMAbortHarness(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/wait" {
			time.Sleep(300 * time.Millisecond)
		}
		io.WriteString(w, "echo:"+string(b))
	})
	c := h.dial()
	defer c.Close()
	br := bufio.NewReader(c)
	host := strings.TrimPrefix(h.origin.URL, "https://")
	read := func(want string) {
		t.Helper()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != want {
			t.Fatalf("body = %q, want %q", b, want)
		}
	}
	// POST with a body sent in two slow pieces on a connection that stays open.
	fmt.Fprintf(c, "POST /a HTTP/1.1\r\nHost: %s\r\nContent-Length: 8\r\n\r\nabcd", host)
	time.Sleep(200 * time.Millisecond)
	fmt.Fprintf(c, "efgh")
	read("echo:abcdefgh")
	// Same connection reused; a quiet-but-alive client during a slow upstream.
	fmt.Fprintf(c, "GET /wait HTTP/1.1\r\nHost: %s\r\n\r\n", host)
	read("echo:")
	// Pipelined: two requests in one write.
	fmt.Fprintf(c, "GET /p1 HTTP/1.1\r\nHost: %s\r\n\r\nGET /wait HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	read("echo:")
	read("echo:")
	flows := waitFlows(t, h.st, 4)
	for _, f := range flows {
		if f.Status != 200 || f.Error != "" {
			t.Fatalf("flow %s %s: status %d error %q", f.Method, f.Path, f.Status, f.Error)
		}
	}
}
