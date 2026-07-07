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
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/tlsca"
)

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
