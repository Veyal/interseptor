package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/strutil"
	"github.com/Veyal/interseptor/internal/tlsca"
)

func TestProxyTransportHasNoResponseHeaderTimeout(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)
	if srv.tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("ResponseHeaderTimeout=%v; want 0 (no limit) so slow upstreams are not cut off", srv.tr.ResponseHeaderTimeout)
	}
}

// wsTextFrame builds an RFC 6455 text frame (payload < 126 bytes).
func wsTextFrame(payload string, masked bool) []byte {
	p := []byte(payload)
	f := []byte{0x81} // FIN + text opcode
	if masked {
		f = append(f, 0x80|byte(len(p)))
		mask := []byte{0x12, 0x34, 0x56, 0x78}
		f = append(f, mask...)
		for i, b := range p {
			f = append(f, b^mask[i%4])
		}
	} else {
		f = append(f, byte(len(p)))
		f = append(f, p...)
	}
	return f
}

func waitWSFrames(t *testing.T, s *store.Store, flowID int64, n int) []*store.WSFrame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fr, _ := s.QueryWSFrames(flowID, 50)
		if len(fr) >= n {
			return fr
		}
		time.Sleep(10 * time.Millisecond)
	}
	fr, _ := s.QueryWSFrames(flowID, 50)
	t.Fatalf("expected %d ws frames, got %d", n, len(fr))
	return nil
}

func waitFlows(t *testing.T, s *store.Store, n int) []*store.Flow {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		flows, _ := s.QueryFlows(50)
		// Flows now appear at request time (pending), so wait until they're
		// filled in — a terminal status or error — before asserting on them.
		if len(flows) >= n && flowsComplete(flows) {
			return flows
		}
		time.Sleep(10 * time.Millisecond)
	}
	flows, _ := s.QueryFlows(50)
	t.Fatalf("expected %d completed flows, got %d", n, len(flows))
	return nil
}

// flowsComplete reports whether every flow has reached a terminal state.
func flowsComplete(flows []*store.Flow) bool {
	for _, f := range flows {
		if f.Status == 0 && f.Error == "" {
			return false
		}
	}
	return true
}

// recEvents records FlowCaptured/FlowUpdated calls, snapshotting status at call
// time because the proxy keeps mutating the same *store.Flow afterward.
type recEvents struct {
	mu       sync.Mutex
	captured []evSnap
	updated  []evSnap
}
type evSnap struct {
	id     int64
	status int
}

func (r *recEvents) FlowCaptured(f *store.Flow) {
	r.mu.Lock()
	r.captured = append(r.captured, evSnap{f.ID, f.Status})
	r.mu.Unlock()
}
func (r *recEvents) FlowUpdated(f *store.Flow) {
	r.mu.Lock()
	r.updated = append(r.updated, evSnap{f.ID, f.Status})
	r.mu.Unlock()
}
func (r *recEvents) snap() (c, u []evSnap) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evSnap(nil), r.captured...), append([]evSnap(nil), r.updated...)
}

// The history row should appear when the request is sent (no status yet) and
// then be updated in place once the response arrives.
func TestProxyRecordsRequestThenResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ev := &recEvents{}
	srv := New(s, capture.New(s), nil, nil, ev)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, u := ev.snap(); len(c) >= 1 && len(u) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, u := ev.snap()
	if len(c) != 1 || c[0].status != 0 {
		t.Fatalf("expected one flow.new with no status, got %+v", c)
	}
	if len(u) != 1 || u[0].status != 200 {
		t.Fatalf("expected one flow.update carrying status 200, got %+v", u)
	}
	if c[0].id != u[0].id {
		t.Fatalf("new/update id mismatch: %d vs %d", c[0].id, u[0].id)
	}
	if flows, _ := s.QueryFlows(10); len(flows) != 1 {
		t.Fatalf("expected exactly 1 persisted row, got %d", len(flows))
	}
}

// In invisible (transparent) proxy mode, a client that isn't configured as a
// proxy client — e.g. traffic redirected via iptables/pf/DNS — sends an
// origin-form request ("GET /path" + Host header). The proxy must forward it to
// the named host instead of rejecting it as a malformed proxy request.
func TestProxyInvisibleModeForwardsOriginForm(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, &recEvents{})
	srv.SetInvisibleProxy(true)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// Send an origin-form request (no absolute URI, no proxy config) directly to
	// the proxy port, naming the upstream in the Host header — exactly what a
	// transparently-redirected client does.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	fmt.Fprintf(conn, "GET /hello HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstreamHost)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want %q", body, "ok")
	}

	flows := waitFlows(t, s, 1)
	f := flows[0]
	uu, _ := url.Parse(upstream.URL)
	upHost := uu.Hostname()
	upPort, _ := strconv.Atoi(uu.Port())
	if upPort == 0 {
		upPort = 80
	}
	if f.Scheme != "http" || f.Host != upHost || f.Port != upPort || f.Path != "/hello" {
		t.Fatalf("captured flow = {scheme:%s host:%s port:%d path:%s}, want http/%s/%d/hello", f.Scheme, f.Host, f.Port, f.Path, upHost, upPort)
	}
}

// With invisible mode off (the default), an origin-form request is rejected.
func TestProxyInvisibleModeOffRejectsOriginForm(t *testing.T) {
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

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /hello HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// Traffic to our own loopback listeners (control plane / proxy) is forwarded but
// must never be recorded — otherwise proxying localhost floods history and
// feedback-loops the live-update SSE.
func TestProxySkipsOwnListenerCapture(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ev := &recEvents{}
	srv := New(s, capture.New(s), nil, nil, ev)
	srv.SelfPorts = []int{9966, 8080}

	// Our own control plane (loopback:9966): not recorded, no events, no id.
	own := &store.Flow{Host: "127.0.0.1", Port: 9966, Method: "GET", Path: "/api/flows", Status: 200}
	srv.recordRequest(own)
	srv.record(own)
	if own.ID != 0 {
		t.Fatalf("own-listener flow must not be inserted, got id=%d", own.ID)
	}

	// localhost by name is loopback too.
	named := &store.Flow{Host: "localhost", Port: 8080, Method: "GET", Path: "/x", Status: 200}
	srv.recordRequest(named)
	if named.ID != 0 {
		t.Fatal("localhost:8080 (own proxy) must not be inserted")
	}

	// A loopback port that ISN'T ours is a legitimate target — record it.
	target := &store.Flow{Host: "127.0.0.1", Port: 8877, Method: "GET", Path: "/app", Status: 200}
	srv.recordRequest(target)
	if target.ID == 0 {
		t.Fatal("a non-self loopback target should be recorded")
	}

	// A normal remote host is always recorded.
	remote := &store.Flow{Host: "example.com", Port: 443, Method: "GET", Path: "/", Status: 200}
	srv.recordRequest(remote)
	if remote.ID == 0 {
		t.Fatal("remote flow should be recorded")
	}

	if flows, _ := s.QueryFlows(10); len(flows) != 2 {
		t.Fatalf("expected only the 2 real targets recorded, got %d", len(flows))
	}
	if c, _ := ev.snap(); len(c) != 2 {
		t.Fatalf("expected 2 flow.new events, got %d", len(c))
	}
}

// Self-traffic must bypass the whole intercept pipeline, not just capture — with
// intercept ON, a request to one of our own listeners must pass straight through
// (never enter the hold queue), or proxying localhost would freeze the UI.
func TestProxyDoesNotInterceptOwnTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	_, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	eng := intercept.New()
	eng.SetEnabled(true) // intercept ON — a normal request would block in the gate
	srv := New(s, capture.New(s), nil, eng, nil)
	srv.SelfPorts = []int{port} // pretend the upstream is one of our own listeners

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 2 * time.Second}
	resp, err := client.Get(upstream.URL + "/health")
	if err != nil {
		t.Fatalf("self-traffic should pass straight through, not be held: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("expected ok, got %q", body)
	}
	if q := eng.Queue(); len(q) != 0 {
		t.Fatalf("self-traffic must not enter the hold queue, got %d held", len(q))
	}
}

func TestConnectUpstreamHost(t *testing.T) {
	cases := []struct {
		name, connectHost, sni, want string
	}{
		{"IP CONNECT target with SNI uses SNI", "192.0.2.10", "connect.example.com", "connect.example.com"},
		{"IPv6 CONNECT target with SNI uses SNI", "2001:db8::1", "connect.example.com", "connect.example.com"},
		{"domain CONNECT target is left untouched even with SNI", "connect.example.com", "connect.example.com", "connect.example.com"},
		{"IP CONNECT target with no SNI falls back to the IP", "192.0.2.10", "", "192.0.2.10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := connectUpstreamHost(c.connectHost, c.sni); got != c.want {
				t.Fatalf("connectUpstreamHost(%q, %q) = %q, want %q", c.connectHost, c.sni, got, c.want)
			}
		})
	}
}

func TestProxyMITMCapturesHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "echo:"+string(body))
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	srv := New(s, capture.New(s), ca, nil, nil)

	// The proxy must trust the test upstream's self-signed cert.
	upstreamPool := x509.NewCertPool()
	upstreamPool.AddCert(upstream.Certificate())
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: upstreamPool}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// The client must trust our CA (it terminates TLS with a minted leaf).
	clientPool := x509.NewCertPool()
	if !clientPool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("add CA to client pool")
	}
	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: clientPool},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Post(upstream.URL+"/submit", "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatalf("https request through proxy: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "echo:ping" {
		t.Fatalf("unexpected response body: %q", got)
	}

	f := waitFlows(t, s, 1)[0]
	if f.Scheme != "https" || f.Method != "POST" || f.Path != "/submit" || f.Status != 200 {
		t.Fatalf("unexpected MITM flow: %+v", f)
	}
	if f.ReqLen != 4 || f.ResBodyHash == "" {
		t.Fatalf("expected captured bodies: reqLen=%d resHash=%q", f.ReqLen, f.ResBodyHash)
	}
}

func TestProxyTunnelsWebSocketUpgrade(t *testing.T) {
	// Minimal upstream that completes a WebSocket-style handshake then echoes.
	upLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upLn.Close()
	go func() {
		c, err := upLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Header.Get("Upgrade") == "" { // the bug: stripped Upgrade header lands here
			io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(c, br) // echo subsequent frames
	}()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))

	fmt.Fprintf(c, "GET http://%s/ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		upLn.Addr().String(), upLn.Addr().String())

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}

	// The tunnel must relay WebSocket frames verbatim.
	frame := wsTextFrame("frame-bytes", true)
	if _, err := c.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := make([]byte, len(frame))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read echoed frame: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("tunnel did not relay the frame verbatim")
	}

	f := waitFlows(t, s, 1)[0]
	if f.Status != http.StatusSwitchingProtocols || f.Flags&store.FlagWebSocket == 0 {
		t.Fatalf("expected 101 + FlagWebSocket, got status=%d flags=%d", f.Status, f.Flags)
	}
	// The frame was captured with its (unmasked) preview.
	frames := waitWSFrames(t, s, f.ID, 2)
	var sawSend bool
	for _, fr := range frames {
		if fr.Dir == "send" && fr.Opcode == 1 && fr.Preview == "frame-bytes" {
			sawSend = true
		}
	}
	if !sawSend {
		t.Fatalf("expected a captured send frame with preview, got %+v", frames)
	}
}

func TestProxyMITMTunnelsWebSocketUpgrade(t *testing.T) {
	// Upstream: a raw TLS listener (signed by upCA) that completes a WS handshake then echoes.
	upCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("upstream CA: %v", err)
	}
	upLeaf, err := upCA.LeafForHost("127.0.0.1")
	if err != nil {
		t.Fatalf("upstream leaf: %v", err)
	}
	upLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*upLeaf}})
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upLn.Close()
	go func() {
		c, err := upLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil || req.Header.Get("Upgrade") == "" {
			io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(c, br)
	}()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
	}
	srv := New(s, capture.New(s), ca, nil, nil)
	upPool := x509.NewCertPool()
	upPool.AppendCertsFromPEM(upCA.CertPEM())
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: upPool} // proxy trusts the upstream

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// Client: CONNECT, then TLS to the proxy's minted leaf, then the WS upgrade.
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upLn.Addr().String(), upLn.Addr().String())
	connResp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, connResp)
	}

	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(ca.CertPEM())
	tlsClient := tls.Client(raw, &tls.Config{RootCAs: clientPool, ServerName: "127.0.0.1"})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client TLS handshake: %v", err)
	}
	fmt.Fprintf(tlsClient, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", upLn.Addr().String())

	wsBr := bufio.NewReader(tlsClient)
	resp, err := http.ReadResponse(wsBr, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read WS handshake: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 over MITM, got %d", resp.StatusCode)
	}
	frame := wsTextFrame("wss-frame", true)
	if _, err := tlsClient.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got := make([]byte, len(frame))
	if _, err := io.ReadFull(wsBr, got); err != nil {
		t.Fatalf("read echoed frame: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("MITM tunnel did not relay the frame verbatim")
	}

	f := waitFlows(t, s, 1)[0]
	if f.Scheme != "https" || f.Status != http.StatusSwitchingProtocols || f.Flags&store.FlagWebSocket == 0 {
		t.Fatalf("unexpected wss flow: scheme=%s status=%d flags=%d", f.Scheme, f.Status, f.Flags)
	}
}

// Live-repro for the MITM (HTTPS) path: the same confused-deputy bug as the
// plain-HTTP path, but here the CONNECT tunnel has already picked destination
// A's TCP/TLS connection before the plaintext request inside it is ever seen.
// Editing the Host header of a held request inside that tunnel must open a
// brand-new outbound connection to the edited (B) target and forward the
// decrypted request there — exactly what Repeater does when sending a request
// wherever its Host says to go — rather than silently keep using A's tunnel.
func TestProxyMITMInterceptEditedHostRetargetsConnection(t *testing.T) {
	upCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("upstream CA: %v", err)
	}

	// Two independent TLS upstreams signed by the same CA, standing in for the
	// audit's two local test targets.
	newTLSTarget := func(body string) net.Listener {
		leaf, err := upCA.LeafForHost("127.0.0.1")
		if err != nil {
			t.Fatalf("leaf: %v", err)
		}
		ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*leaf}})
		if err != nil {
			t.Fatalf("upstream listen: %v", err)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					br := bufio.NewReader(c)
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					req.Body.Close()
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				}()
			}
		}()
		return ln
	}
	targetA := newTLSTarget("response-from-A")
	defer targetA.Close()
	targetB := newTLSTarget("response-from-B")
	defer targetB.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
	}
	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), ca, eng, nil)
	upPool := x509.NewCertPool()
	upPool.AppendCertsFromPEM(upCA.CertPEM())
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: upPool} // proxy trusts both upstreams

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// Client CONNECTs to target A and completes the MITM TLS handshake.
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetA.Addr().String(), targetA.Addr().String())
	connResp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, connResp)
	}
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(ca.CertPEM())
	tlsClient := tls.Client(raw, &tls.Config{RootCAs: clientPool, ServerName: "127.0.0.1"})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client TLS handshake: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		fmt.Fprintf(tlsClient, "GET /secret.txt HTTP/1.1\r\nHost: %s\r\n\r\n", targetA.Addr().String())
		br := bufio.NewReader(tlsClient)
		resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}

	// The operator edits the raw request's Host header to target B before forwarding.
	edited := fmt.Sprintf("GET /secret.txt HTTP/1.1\r\nHost: %s\r\n\r\n", targetB.Addr().String())
	if err := eng.Forward(eng.Queue()[0].ID, []byte(edited)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}

	select {
	case body := <-done:
		if body != "response-from-B" {
			t.Fatalf("editing Host over MITM must retarget the outbound connection: got body %q, want %q", body, "response-from-B")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	bHost, bPortStr, _ := net.SplitHostPort(targetB.Addr().String())
	bPort, _ := strconv.Atoi(bPortStr)
	f := waitFlows(t, s, 1)[0]
	if f.Host != bHost || f.Port != bPort {
		t.Fatalf("recorded flow target = %s:%d, want the real destination %s:%d", f.Host, f.Port, bHost, bPort)
	}
}

// Security regression for the HTTPS-MITM path: the same self-listener guard
// proven over plain HTTP in TestProxyInterceptEditedHostToOwnListenerIsRefused
// must also hold inside the MITM tunnel. The CONNECT tunnel picks target A's
// TLS connection up front, but the held plaintext request inside it can still
// be edited to point Host at Interseptor's own loopback listener before
// forwarding — gateAndForward is the single funnel point for both the
// plain-HTTP and MITM paths, so this proves the guard added there covers both.
func TestProxyMITMInterceptEditedHostToOwnListenerIsRefused(t *testing.T) {
	upCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("upstream CA: %v", err)
	}
	leaf, err := upCA.LeafForHost("127.0.0.1")
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	targetA, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*leaf}})
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer targetA.Close()
	go func() {
		for {
			c, err := targetA.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				req.Body.Close()
				body := "response-from-A"
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
			}()
		}
	}()

	// Stand in for Interseptor's own listener. This MUST speak TLS on the same
	// CA the proxy's transport trusts: gateAndForward carries flow.Scheme
	// ("https") through an edited Host unchanged, so inside a MITM session
	// the retargeted dial is always TLS. Using a plain-HTTP stand-in here
	// would make the TLS handshake itself the thing that blocks the request,
	// masking whether the self-listener guard is actually doing its job — a
	// TLS-speaking stand-in isolates the guard as the only possible blocker.
	var ownHits int32
	ownLeaf, err := upCA.LeafForHost("127.0.0.1")
	if err != nil {
		t.Fatalf("own leaf: %v", err)
	}
	ownLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{*ownLeaf}})
	if err != nil {
		t.Fatalf("own listen: %v", err)
	}
	defer ownLn.Close()
	go func() {
		for {
			c, err := ownLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				req.Body.Close()
				atomic.AddInt32(&ownHits, 1)
				body := `{"keys":["leaked-secret"]}`
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
			}()
		}
	}()
	_, ownPortStr, _ := net.SplitHostPort(ownLn.Addr().String())
	ownPort, _ := strconv.Atoi(ownPortStr)

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
	}
	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), ca, eng, nil)
	srv.SelfPorts = []int{ownPort}
	upPool := x509.NewCertPool()
	upPool.AppendCertsFromPEM(upCA.CertPEM())
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: upPool}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetA.Addr().String(), targetA.Addr().String())
	connResp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v / %v", err, connResp)
	}
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(ca.CertPEM())
	tlsClient := tls.Client(raw, &tls.Config{RootCAs: clientPool, ServerName: "127.0.0.1"})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client TLS handshake: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		fmt.Fprintf(tlsClient, "GET /secret.txt HTTP/1.1\r\nHost: %s\r\n\r\n", targetA.Addr().String())
		br := bufio.NewReader(tlsClient)
		resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}

	// The operator (or a prompt-injected AI agent) edits Host inside the MITM
	// tunnel to Interseptor's own listener and asks to forward — refused.
	edited := fmt.Sprintf("GET /api/keys HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", ownPort)
	if err := eng.Forward(eng.Queue()[0].ID, []byte(edited)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}

	select {
	case body := <-done:
		if strings.Contains(body, "leaked-secret") {
			t.Fatalf("own-listener response body leaked back to the client over the MITM tunnel: %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	if n := atomic.LoadInt32(&ownHits); n != 0 {
		t.Fatalf("own listener received %d request(s) over the MITM path; the guard must refuse before ever dialing it", n)
	}
}

func TestUpstreamProxyConfig(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)

	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.com"}}
	if u, _ := srv.tr.Proxy(req); u != nil {
		t.Fatalf("default should be direct, got %v", u)
	}
	if err := srv.SetUpstreamProxy("http://corp:3128"); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	if u, _ := srv.tr.Proxy(req); u == nil || u.Host != "corp:3128" {
		t.Fatalf("expected upstream corp:3128, got %v", u)
	}
	if err := srv.SetUpstreamProxy(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if u, _ := srv.tr.Proxy(req); u != nil {
		t.Fatalf("expected direct after clear, got %v", u)
	}
	if err := srv.SetUpstreamProxy("://bad"); err == nil {
		t.Fatal("expected error for invalid upstream URL")
	}
}

func TestUpstreamProxyAddressDefaultsPorts(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"http://proxy.example", "proxy.example:80"},
		{"https://proxy.example", "proxy.example:443"},
		{"http://proxy.example:3128", "proxy.example:3128"},
	} {
		up, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", tc.raw, err)
		}
		if got := upstreamProxyAddress(up); got != tc.want {
			t.Errorf("upstreamProxyAddress(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestSetUpstreamProxyNormalizesSchemeCase(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		wantScheme string
		wantAddr   string
	}{
		{"HTTP://proxy.example", "http", "proxy.example:80"},
		{"HtTpS://proxy.example", "https", "proxy.example:443"},
	} {
		srv := New(nil, nil, nil, nil, nil)
		if err := srv.SetUpstreamProxy(tc.raw); err != nil {
			t.Fatalf("SetUpstreamProxy(%q): %v", tc.raw, err)
		}
		up := srv.upstream.Load()
		if up.Scheme != tc.wantScheme {
			t.Errorf("scheme for %q = %q, want %q", tc.raw, up.Scheme, tc.wantScheme)
		}
		if got := upstreamProxyAddress(up); got != tc.wantAddr {
			t.Errorf("address for %q = %q, want %q", tc.raw, got, tc.wantAddr)
		}
	}
	mixed := &url.URL{Scheme: "HtTpS", Host: "proxy.example"}
	if got := upstreamProxyAddress(mixed); got != "proxy.example:443" {
		t.Errorf("mixed-case HTTPS address = %q, want proxy.example:443", got)
	}
}

func TestDialViaHTTPSUpstreamUsesTLSAndSNI(t *testing.T) {
	proxyCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
	}
	leaf, err := proxyCA.LeafForHost("localhost")
	if err != nil {
		t.Fatalf("proxy leaf: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	gotSNI := make(chan string, 1)
	gotAuth := make(chan string, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		tc := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{*leaf},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				gotSNI <- hello.ServerName
				return nil, nil
			},
		})
		req, err := http.ReadRequest(bufio.NewReader(tc))
		if err != nil {
			return
		}
		gotAuth <- req.Header.Get("Proxy-Authorization")
		io.WriteString(tc, "HTTP/1.1 200 Connection Established\r\n\r\n")
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	srv := New(nil, nil, nil, nil, nil)
	if err := srv.SetUpstreamProxy("HtTpS://alice:secret@localhost:" + port); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	up := srv.upstream.Load()
	up.Scheme = "HtTpS" // custom callers may supply a URL without net/url normalization
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(proxyCA.CertPEM())
	d := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialViaUpstream(d, up, "origin.example:443", &tls.Config{RootCAs: roots})
	if err != nil {
		t.Fatalf("dialViaUpstream: %v", err)
	}
	conn.Close()

	if got := <-gotSNI; got != "localhost" {
		t.Fatalf("proxy TLS SNI = %q, want localhost", got)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	if got := <-gotAuth; got != wantAuth {
		t.Fatalf("Proxy-Authorization = %q, want %q", got, wantAuth)
	}
}

func TestDialViaUpstreamTimesOutSilentCONNECTAndClosesConnection(t *testing.T) {
	proxyCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
	}
	leaf, err := proxyCA.LeafForHost("localhost")
	if err != nil {
		t.Fatalf("proxy leaf: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(proxyCA.CertPEM())

	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer ln.Close()

			accepted := make(chan net.Conn, 1)
			requestRead := make(chan error, 1)
			connectionClosed := make(chan error, 1)
			go func() {
				raw, err := ln.Accept()
				if err != nil {
					requestRead <- err
					return
				}
				accepted <- raw
				defer raw.Close()
				conn := raw
				if scheme == "https" {
					tc := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{*leaf}})
					if err := tc.Handshake(); err != nil {
						requestRead <- err
						return
					}
					conn = tc
				}
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					requestRead <- err
					return
				}
				requestRead <- nil
				_, err = conn.Read(make([]byte, 1))
				connectionClosed <- err
			}()

			_, port, _ := net.SplitHostPort(ln.Addr().String())
			up, _ := url.Parse(scheme + "://localhost:" + port)
			d := &net.Dialer{Timeout: 80 * time.Millisecond}
			result := make(chan error, 1)
			started := time.Now()
			go func() {
				conn, err := dialViaUpstream(d, up, "origin.example:443", &tls.Config{RootCAs: roots})
				if conn != nil {
					conn.Close()
				}
				result <- err
			}()

			if err := <-requestRead; err != nil {
				t.Fatalf("read CONNECT request: %v", err)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("silent upstream unexpectedly completed CONNECT")
				}
				netErr, ok := err.(net.Error)
				if !ok || !netErr.Timeout() {
					t.Fatalf("dial error = %v, want timeout", err)
				}
				if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
					t.Fatalf("CONNECT timeout took %v, want bounded by dialer timeout", elapsed)
				}
			case <-time.After(500 * time.Millisecond):
				raw := <-accepted
				raw.Close()
				<-result
				t.Fatal("silent upstream CONNECT did not time out")
			}

			select {
			case err := <-connectionClosed:
				if err == nil {
					t.Fatal("silent upstream connection remained open")
				}
			case <-time.After(time.Second):
				t.Fatal("upstream did not observe client connection cleanup")
			}
		})
	}
}

func TestProxyResponseMatchReplace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "the topsecret value")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "topsecret", Replace: "REDACTED"}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	srv := New(s, capture.New(s), nil, eng, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	resp, err := client.Get(upstream.URL + "/x")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "the REDACTED value" {
		t.Fatalf("response-side match-&-replace not applied: %q", body)
	}
}

func TestProxyInterceptHoldThenForward(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), nil, eng, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	done := make(chan string, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/held")
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	// Wait until the request is sitting in the hold queue, then forward it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}
	if err := eng.Forward(eng.Queue()[0].ID, nil); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	select {
	case body := <-done:
		if body != "ok" {
			t.Fatalf("unexpected client body: %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	f := waitFlows(t, s, 1)[0]
	if f.Flags&store.FlagIntercepted == 0 {
		t.Fatalf("expected FlagIntercepted set, flags=%d", f.Flags)
	}
}

// Live-repro regression for the confused-deputy / vhost-smuggling bug: holding
// a request to one target and editing its Host header before forwarding must
// actually retarget the TCP connection to the new host — not silently keep
// connecting to the original target while claiming (on the wire) to be the
// edited one. Two independent local upstreams stand in for the audit's two
// local test targets.
func TestProxyInterceptEditedHostRetargetsConnection(t *testing.T) {
	targetA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "response-from-A")
	}))
	defer targetA.Close()
	targetB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "response-from-B")
	}))
	defer targetB.Close()

	uA, _ := url.Parse(targetA.URL)
	uB, _ := url.Parse(targetB.URL)

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), nil, eng, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	done := make(chan string, 1)
	go func() {
		// The client asks for target A...
		resp, err := client.Get(targetA.URL + "/secret.txt")
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}

	// ...but the operator edits the raw request's Host header to target B
	// before forwarding.
	edited := fmt.Sprintf("GET /secret.txt HTTP/1.1\r\nHost: %s\r\n\r\n", uB.Host)
	if err := eng.Forward(eng.Queue()[0].ID, []byte(edited)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}

	select {
	case body := <-done:
		// The connection must actually go to B now — not A.
		if body != "response-from-B" {
			t.Fatalf("editing Host must retarget the connection: got body %q, want %q (confused-deputy: connected to A but claimed to be B)", body, "response-from-B")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	f := waitFlows(t, s, 1)[0]
	if f.Flags&store.FlagEdited == 0 {
		t.Fatalf("expected FlagEdited set, flags=%d", f.Flags)
	}
	// History must stay honest about where the request actually went — not the
	// stale pre-edit target.
	if f.Host != uB.Hostname() {
		t.Fatalf("recorded flow.Host = %q, want the real destination %q", f.Host, uB.Hostname())
	}
	wantPort, _ := strconv.Atoi(uB.Port())
	if f.Port != wantPort {
		t.Fatalf("recorded flow.Port = %d, want the real destination port %d", f.Port, wantPort)
	}
	if got := f.ReqHeaders["Host"]; len(got) != 1 || got[0] != uB.Host {
		t.Fatalf("recorded wire Host header = %v, want [%q]", got, uB.Host)
	}
	// targetA must never have seen this request.
	if f.Host == uA.Hostname() && f.Port == func() int { p, _ := strconv.Atoi(uA.Port()); return p }() {
		t.Fatal("flow still recorded against the original (pre-edit) target")
	}
}

// Regression: an UNEDITED held request must still route to the original host
// (existing behavior preserved) — this is the common case and must not
// regress when the edited-Host retargeting fix lands.
func TestProxyInterceptUneditedHoldStillRoutesToOriginalHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok-from-original")
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), nil, eng, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	done := make(chan string, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/held")
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}
	// Forward the held raw dump completely unedited (round-tripped verbatim).
	if err := eng.Forward(eng.Queue()[0].ID, eng.Queue()[0].Raw); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	select {
	case body := <-done:
		if body != "ok-from-original" {
			t.Fatalf("unedited hold should still route to the original host: got %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	uOrig, _ := url.Parse(upstream.URL)
	f := waitFlows(t, s, 1)[0]
	if f.Host != uOrig.Hostname() {
		t.Fatalf("flow.Host = %q, want original host %q", f.Host, uOrig.Hostname())
	}
}

// Security regression: the Host-retargeting fix (see
// TestProxyInterceptEditedHostRetargetsConnection) lets an operator edit a held
// request's Host header to genuinely redirect the outbound connection — but it
// never checked whether the new target is Interseptor's OWN loopback listener
// (control plane or proxy). Without this guard, an MCP-driving AI agent (or
// prompt-injected content reaching one) could hold a request via set_intercept,
// then call forward_request with Host: 127.0.0.1:<control-port> and a
// control-API path (e.g. GET /api/keys). The proxy would dial that address for
// real; because the resulting connection is genuinely loopback-sourced with a
// loopback Host header, the control API's unauthenticated-loopback trust path
// (internal/control/guard.go) would grant it full access — reading API keys,
// findings, captured credentials, settings — all disguised as an ordinary
// "forward this request" action. This is the same class of attack that
// Repeater/Intruder/WS-repeater/the AI agent tool already refuse via
// targetsOwnListener/isOwnListener (internal/control/activescan.go); this test
// asserts the Intercept-forward path gets the same protection, enforced against
// the FINAL (post-edit) target rather than the pre-edit one.
func TestProxyInterceptEditedHostToOwnListenerIsRefused(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), nil, eng, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	// Stand in for Interseptor's own control-plane listener: a real HTTP
	// server on a loopback port that srv.SelfPorts knows about. If the guard
	// fails to refuse, this handler would actually receive the "attack"
	// request and reply with a distinctive body proving the leak.
	var ownHits int32
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ownHits, 1)
		io.WriteString(w, `{"keys":["leaked-secret"]}`)
	}))
	defer own.Close()
	uOwn, _ := url.Parse(own.URL)
	ownPort, _ := strconv.Atoi(uOwn.Port())
	srv.SelfPorts = []int{ownPort}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "response-from-upstream")
	}))
	defer upstream.Close()

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	type result struct {
		status int
		body   string
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/held")
		if err != nil {
			done <- result{0, "err:" + err.Error()}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- result{resp.StatusCode, string(b)}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}

	// The operator (or a prompt-injected AI agent) edits Host to Interseptor's
	// own listener and asks to forward a crafted control-API request — this
	// must be refused, not dialed.
	edited := fmt.Sprintf("GET /api/keys HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", ownPort)
	if err := eng.Forward(eng.Queue()[0].ID, []byte(edited)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}

	select {
	case r := <-done:
		if r.status < 400 {
			t.Fatalf("expected the forward to be refused (>=400), got status=%d body=%q", r.status, r.body)
		}
		if strings.Contains(r.body, "leaked-secret") {
			t.Fatalf("own-listener response body leaked back to the client: %q", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	// The strongest assertion: Interseptor's own listener must never have
	// received the "attack" request at all — the guard refuses before dialing.
	if n := atomic.LoadInt32(&ownHits); n != 0 {
		t.Fatalf("own listener received %d request(s); the guard must refuse before ever dialing it", n)
	}
}

// Regression: editing Host to a normal, non-self target must keep working
// exactly as TestProxyInterceptEditedHostRetargetsConnection already proves —
// the self-listener guard must not over-trigger on legitimate retargets. This
// test exercises the same scenario end-to-end once more with an explicit
// SelfPorts set (disjoint from both targets), to prove the guard only refuses
// genuine self-targeting, not retargeting in general.
func TestProxyInterceptEditedHostToNonSelfTargetStillForwards(t *testing.T) {
	targetA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "response-from-A")
	}))
	defer targetA.Close()
	targetB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "response-from-B")
	}))
	defer targetB.Close()

	uA, _ := url.Parse(targetA.URL)
	uB, _ := url.Parse(targetB.URL)

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(s, capture.New(s), nil, eng, nil)
	// A SelfPorts entry that matches neither target — the guard must not refuse
	// a legitimate retarget just because SelfPorts is non-empty.
	srv.SelfPorts = []int{1}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}

	done := make(chan string, 1)
	go func() {
		resp, err := client.Get(targetA.URL + "/secret.txt")
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- string(b)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.Queue()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(eng.Queue()) != 1 {
		t.Fatalf("expected 1 held request, got %d", len(eng.Queue()))
	}

	edited := fmt.Sprintf("GET /secret.txt HTTP/1.1\r\nHost: %s\r\n\r\n", uB.Host)
	if err := eng.Forward(eng.Queue()[0].ID, []byte(edited)); err != nil {
		t.Fatalf("Forward edited: %v", err)
	}

	select {
	case body := <-done:
		if body != "response-from-B" {
			t.Fatalf("editing Host to a non-self target must still retarget the connection: got body %q, want %q", body, "response-from-B")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client never got a response after forward")
	}

	f := waitFlows(t, s, 1)[0]
	if f.Host != uB.Hostname() {
		t.Fatalf("recorded flow.Host = %q, want the real destination %q", f.Host, uB.Hostname())
	}
	if f.Host == uA.Hostname() && f.Port == strutil.AtoiOr(uA.Port(), 0) {
		t.Fatal("flow still recorded against the original (pre-edit) target")
	}
}

func TestProxyForwardsAndCapturesFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		io.WriteString(w, "echo:"+string(body))
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Post(upstream.URL+"/submit", "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "echo:ping" {
		t.Fatalf("unexpected response body: %q", got)
	}

	// Allow the proxy goroutine to finish recording the flow (it appears at
	// request time and is updated once the response is captured).
	deadline := time.Now().Add(2 * time.Second)
	var flows []*store.Flow
	for time.Now().Before(deadline) {
		flows, _ = s.QueryFlows(10)
		if len(flows) == 1 && flowsComplete(flows) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 captured flow, got %d", len(flows))
	}
	f := flows[0]
	if f.Method != "POST" || f.Status != 200 || f.Path != "/submit" {
		t.Fatalf("unexpected flow: %+v", f)
	}
	if f.ReqLen != 4 { // "ping"
		t.Fatalf("expected req body len 4, got %d", f.ReqLen)
	}
	if f.ResBodyHash == "" {
		t.Fatal("expected response body to be captured")
	}
}

func TestProxyRecordsErroredFlowOnUpstreamFailure(t *testing.T) {
	// A definitely-refused upstream: bind a port, then close it.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Get("http://" + deadAddr + "/gone")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 to client, got %d", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	var flows []*store.Flow
	for time.Now().Before(deadline) {
		flows, _ = s.QueryFlows(10)
		if len(flows) == 1 && flowsComplete(flows) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 errored flow, got %d", len(flows))
	}
	f := flows[0]
	if f.Status != http.StatusBadGateway {
		t.Fatalf("expected flow status 502, got %d", f.Status)
	}
	if f.Error == "" {
		t.Fatal("expected non-empty Error on errored flow")
	}
	if f.Method != "GET" || f.Path != "/gone" {
		t.Fatalf("unexpected errored flow: %+v", f)
	}
}
