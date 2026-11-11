package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// tcpOrigin runs handle for every accepted connection of a loopback listener.
func tcpOrigin(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); handle(c) }()
		}
	}()
	return ln.Addr().String()
}

func r2Proxy(t *testing.T, eng *intercept.Engine, withCA bool) (*Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var ca *tlsca.CA
	if withCA {
		if ca, err = tlsca.LoadOrCreate(t.TempDir()); err != nil {
			t.Fatalf("CA: %v", err)
		}
	}
	srv := New(st, capture.New(st), ca, eng, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	return srv, st, ln.Addr().String()
}

const wsUpgradeRequest = "GET http://%s/ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"

// An origin that accepts the connection, reads the upgrade request and never
// answers must not hold the client forever: the wait for the handshake
// response is bounded and reported as a 502.
func TestUpgradeToSilentUpstreamIsBounded(t *testing.T) {
	origin := tcpOrigin(t, func(c net.Conn) { io.Copy(io.Discard, c) }) // read, never answer
	srv, st, addr := r2Proxy(t, nil, false)
	srv.upgradeResponse = 150 * time.Millisecond

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, wsUpgradeRequest, origin, origin)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("client is still waiting on an upgrade the origin never answered: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	f := waitFlows(t, st, 1)[0]
	if !strings.HasPrefix(f.Error, "upgrade response:") {
		t.Fatalf("flow error = %q, want an upgrade response timeout", f.Error)
	}
}

// The bound covers the handshake only: an upgraded stream that then stays idle
// for longer than the bound keeps working.
func TestUpgradeBoundDoesNotCutIdleStream(t *testing.T) {
	origin := tcpOrigin(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: raw-test\r\n\r\n")
		time.Sleep(400 * time.Millisecond) // idle well past the handshake bound
		io.WriteString(c, "late")
		io.Copy(io.Discard, br)
	})
	srv, _, addr := r2Proxy(t, nil, false)
	srv.upgradeResponse = 100 * time.Millisecond

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, strings.Replace(wsUpgradeRequest, "websocket", "raw-test", 1), origin, origin)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: resp=%v err=%v", resp, err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "late" {
		t.Fatalf("idle upgraded stream was cut: %q, %v", got, err)
	}
}

// bypassTunnel opens a raw TLS-passthrough tunnel (no TLS needed to exercise
// the splice: the proxy never looks inside it).
func bypassTunnel(t *testing.T, srv *Server, proxyAddr, origin string) *net.TCPConn {
	t.Helper()
	host, _, _ := net.SplitHostPort(origin)
	srv.SetTLSBypassHosts([]string{host})
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", origin, origin)
	// Read the CONNECT reply byte by byte so nothing of the tunnel is buffered.
	var head []byte
	one := make([]byte, 1)
	for !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
		if _, err := c.Read(one); err != nil {
			t.Fatalf("CONNECT reply: %v", err)
		}
		head = append(head, one[0])
	}
	if !bytes.HasPrefix(head, []byte("HTTP/1.1 200")) {
		t.Fatalf("CONNECT reply = %q", head)
	}
	return c.(*net.TCPConn)
}

// A client that sends its request and shuts down its write side (FIN) must
// still receive the response: the passthrough tunnel used to be torn down as
// soon as either direction reached EOF.
func TestTLSPassthroughSupportsClientHalfClose(t *testing.T) {
	origin := tcpOrigin(t, func(c net.Conn) {
		req, _ := io.ReadAll(c) // returns only once the client's FIN arrives
		fmt.Fprintf(c, "got %d bytes: %s", len(req), req)
	})
	srv, _, addr := r2Proxy(t, nil, true)
	c := bypassTunnel(t, srv, addr, origin)
	io.WriteString(c, "request")
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read response after half-close: %v", err)
	}
	if string(got) != "got 7 bytes: request" {
		t.Fatalf("response after client half-close = %q, want the origin's answer", got)
	}
}

// The other direction: an origin that answers and half-closes while the client
// keeps sending must keep receiving the client's bytes.
func TestTLSPassthroughSupportsOriginHalfClose(t *testing.T) {
	gotc := make(chan string, 1)
	origin := tcpOrigin(t, func(c net.Conn) {
		io.WriteString(c, "hello")
		c.(*net.TCPConn).CloseWrite()
		rest, _ := io.ReadAll(c)
		gotc <- string(rest)
	})
	srv, _, addr := r2Proxy(t, nil, true)
	c := bypassTunnel(t, srv, addr, origin)
	if b, err := io.ReadAll(c); err != nil || string(b) != "hello" {
		t.Fatalf("origin greeting = %q, %v", b, err)
	}
	io.WriteString(c, "late client data")
	c.Close()
	select {
	case got := <-gotc:
		if got != "late client data" {
			t.Fatalf("origin received %q after its half-close", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("origin never saw the client's data after half-closing")
	}
}

// After a half-close the remaining direction is bounded, so an origin that
// neither answers nor closes cannot pin the tunnel.
func TestTLSPassthroughHalfClosedTunnelIsReapedWhenIdle(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	origin := tcpOrigin(t, func(c net.Conn) { <-release }) // never answers, never closes
	srv, _, addr := r2Proxy(t, nil, true)
	srv.halfCloseIdle = 150 * time.Millisecond
	c := bypassTunnel(t, srv, addr, origin)
	io.WriteString(c, "request")
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("half-closed idle tunnel was not torn down: %v", err)
	}
}

// failingResponseWriter reports the client as gone on the body write.
type failingResponseWriter struct{ h http.Header }

func (w *failingResponseWriter) Header() http.Header       { return w.h }
func (w *failingResponseWriter) WriteHeader(int)           {}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

type failingConn struct{ net.Conn }

func (failingConn) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func rewriteRig(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "topsecret", Replace: "REDACTED"}}); err != nil {
		t.Fatal(err)
	}
	return New(st, capture.New(st), nil, eng, nil), st
}

func rewriteResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Proto: "HTTP/1.1",
		Header:        http.Header{"Content-Type": {"text/plain"}},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
		Request:       httptest.NewRequest(http.MethodGet, "http://example.com/x", nil),
	}
}

// A rewritten response whose client went away before it was delivered must be
// flagged, exactly like the streaming path, instead of being recorded as a
// normal completed exchange.
func TestTransformedResponseWriteErrorIsFlagged(t *testing.T) {
	t.Run("plain HTTP", func(t *testing.T) {
		srv, _ := rewriteRig(t)
		flow := srv.newFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "http", Host: "example.com", Port: 80, Path: "/x"})
		srv.writeResponseHTTP(&failingResponseWriter{h: http.Header{}}, rewriteResponse("the topsecret value"), flow)
		if flow.Flags&store.FlagCaptureError == 0 || !strings.HasPrefix(flow.Error, "stream resp:") {
			t.Fatalf("flags=%v error=%q, want FlagCaptureError and a stream resp error", flow.Flags, flow.Error)
		}
	})
	t.Run("MITM tunnel", func(t *testing.T) {
		srv, _ := rewriteRig(t)
		flow := srv.newFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "example.com", Port: 443, Path: "/x"})
		if err := srv.writeResponseConn(failingConn{}, rewriteResponse("the topsecret value"), flow); err == nil {
			t.Fatal("write error was not returned")
		}
		if flow.Flags&store.FlagCaptureError == 0 || !strings.HasPrefix(flow.Error, "stream resp:") {
			t.Fatalf("flags=%v error=%q, want FlagCaptureError and a stream resp error", flow.Flags, flow.Error)
		}
	})
	t.Run("delivered response is not flagged", func(t *testing.T) {
		srv, _ := rewriteRig(t)
		flow := srv.newFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "http", Host: "example.com", Port: 80, Path: "/x"})
		rec := httptest.NewRecorder()
		srv.writeResponseHTTP(rec, rewriteResponse("the topsecret value"), flow)
		if flow.Flags&store.FlagCaptureError != 0 || flow.Error != "" || rec.Body.String() != "the REDACTED value" {
			t.Fatalf("flags=%v error=%q body=%q", flow.Flags, flow.Error, rec.Body.String())
		}
	})
}
