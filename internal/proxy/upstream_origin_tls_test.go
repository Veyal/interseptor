package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// connectRecord is one CONNECT request seen by newConnectRelay.
type connectRecord struct {
	target string
	auth   string
}

// newConnectRelay starts an HTTP CONNECT proxy that tunnels to the requested
// target and reports every CONNECT it serves.
func newConnectRelay(t *testing.T) (string, <-chan connectRecord) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen CONNECT fixture: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	seen := make(chan connectRecord, 8)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go relayCONNECT(client, seen)
		}
	}()
	return ln.Addr().String(), seen
}

func relayCONNECT(client net.Conn, seen chan<- connectRecord) {
	defer client.Close()
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	seen <- connectRecord{target: req.Host, auth: req.Header.Get("Proxy-Authorization")}
	if req.Method != http.MethodConnect {
		_, _ = io.WriteString(client, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	origin, err := net.DialTimeout("tcp", req.Host, 3*time.Second)
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer origin.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(origin, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, origin); done <- struct{}{} }()
	<-done
}

func roundTripHTTPS(t *testing.T, srv *Server, rawURL string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return srv.tr.RoundTrip(req)
}

func TestHTTPUpstreamHonorsOriginTLSVerifyOff(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "self-signed origin")
	}))
	defer origin.Close()
	relay, seen := newConnectRelay(t)

	srv := New(nil, nil, nil, nil, nil)
	srv.SetOriginTLSVerify(false)
	if err := srv.SetUpstreamProxy("http://" + relay); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	resp, err := roundTripHTTPS(t, srv, origin.URL)
	if err != nil {
		t.Fatalf("HTTPS through HTTP upstream with origin verify off: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "self-signed origin" {
		t.Fatalf("body = %q", body)
	}
	select {
	case rec := <-seen:
		if rec.target != origin.Listener.Addr().String() {
			t.Fatalf("CONNECT target = %q, want %q", rec.target, origin.Listener.Addr().String())
		}
	default:
		t.Fatal("request bypassed the HTTP upstream proxy")
	}
}

func TestHTTPUpstreamHonorsOriginTLSVerifyExceptions(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer origin.Close()
	relay, seen := newConnectRelay(t)

	srv := New(nil, nil, nil, nil, nil)
	srv.SetOriginTLSVerify(true)
	if err := srv.SetUpstreamProxy("http://" + relay); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	if resp, err := roundTripHTTPS(t, srv, origin.URL); err == nil {
		resp.Body.Close()
		t.Fatal("strict origin verification accepted a self-signed origin through the upstream")
	}
	<-seen
	srv.SetOriginTLSVerifyBypassHosts([]string{"127.0.0.1"})
	resp, err := roundTripHTTPS(t, srv, origin.URL)
	if err != nil {
		t.Fatalf("origin TLS exception ignored through HTTP upstream: %v", err)
	}
	resp.Body.Close()
	<-seen
}
