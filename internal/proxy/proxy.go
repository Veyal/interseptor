// Package proxy implements an intercepting HTTP/HTTPS forward proxy that
// captures every flow. Plain HTTP is forwarded directly; HTTPS is intercepted
// via CONNECT + a locally-minted leaf certificate (TLS MITM).
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/plugin"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/strutil"
	"github.com/Veyal/interseptor/internal/tlsca"
	"golang.org/x/net/proxy"
)

// Events is an optional sink notified when a flow is captured (used by the
// control plane to push live updates to the UI).
type Events interface {
	FlowCaptured(*store.Flow) // a new flow (request seen / sent upstream)
	FlowUpdated(*store.Flow)  // an existing flow filled in (response, error, …)
}

// ScopeChecker reports whether a flow is in the engagement's target scope.
// When set, only in-scope requests are held by the intercept gate.
type ScopeChecker interface {
	InScope(*store.Flow) bool
}

// Server is the intercepting forward-proxy HTTP handler.
type Server struct {
	st                       *store.Store
	cap                      *capture.Capturer
	ca                       *tlsca.CA         // nil → HTTPS CONNECT returns 501
	eng                      *intercept.Engine // nil → no intercept/rules
	events                   Events            // nil → no live notifications
	Scope                    ScopeChecker      // nil → everything in scope
	tr                       *http.Transport
	upstream                 atomic.Pointer[url.URL] // optional chained upstream proxy
	upstreamProxyRoots       atomic.Pointer[x509.CertPool]
	scopeOnly                atomic.Bool // when set, only in-scope flows are persisted
	suppressTelemetry        atomic.Bool // when set, browser telemetry is not captured or intercepted
	suppressAndroidTelemetry atomic.Bool // when set, Android/GMS/Crashlytics telemetry is not captured or intercepted
	invisible                atomic.Bool // when set, origin-form requests (no absolute URI) are forwarded from the Host header

	// TLS-bypass: CONNECTs to a matching host are tunneled raw (no MITM) so the
	// client's pinning/handshake reaches the real origin and the app keeps working.
	bypassHosts   atomic.Pointer[[]string] // host patterns to pass through untouched
	bypassVersion atomic.Uint64            // detects a newer list while a callback is running
	bypassMu      sync.Mutex               // serializes bypass-list read-modify-write
	autoBypass    atomic.Bool              // add a host to bypassHosts when its MITM handshake fails (pinning)
	bypassSeen    sync.Map                 // host → struct{}: hosts already logged as bypassed (dedupe the info flow)
	// OnBypassAdded is fired (outside locks) after autoBypass appends a host, with
	// the full updated list, so the control plane can persist it and refresh the UI.
	OnBypassAdded func([]string)

	// SelfPorts are this tool's own loopback ports (control plane + proxy). Set
	// by cmd; traffic to them is forwarded but never recorded, so proxying
	// localhost doesn't fill history with — or feedback-loop on — our own UI.
	SelfPorts []int
}

// New builds a proxy Server. ca, eng, and events may each be nil.
func New(st *store.Store, cap *capture.Capturer, ca *tlsca.CA, eng *intercept.Engine, events Events) *Server {
	s := &Server{st: st, cap: cap, ca: ca, eng: eng, events: events}
	s.tr = &http.Transport{
		// Honor an optionally-configured chained upstream proxy (race-safe).
		Proxy: func(*http.Request) (*url.URL, error) {
			up := s.upstream.Load()
			if up != nil && isSOCKSUpstream(up) {
				return nil, nil
			}
			return up, nil
		},
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
		// No ResponseHeaderTimeout: slow/long-polling upstreams must not be cut
		// off by the proxy. Dial still has its own connect bound.
		// Prefer HTTP/2 to the origin when ALPN negotiates it. The MITM leg to
		// the client stays HTTP/1.1 (see writeResponseConn ProtoMajor downgrade)
		// so forwarding never hangs; History records the upstream proto.
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
		},
		DialTLSContext: s.dialTLSContext,
		DialContext:    s.dialContext,
	}
	return s
}

// SetUpstreamProxyCA adds PEM-encoded certificates to roots used only for an
// HTTPS chained upstream proxy. Empty input restores system roots.
func (s *Server) SetUpstreamProxyCA(pemBytes []byte) error {
	var roots *x509.CertPool
	if len(bytes.TrimSpace(pemBytes)) != 0 {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("load system certificate roots: %w", err)
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return fmt.Errorf("invalid upstream proxy CA PEM")
		}
	}
	s.upstreamProxyRoots.Store(roots)
	s.tr.CloseIdleConnections()
	return nil
}

func (s *Server) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if up := s.upstream.Load(); up != nil && isSOCKSUpstream(up) {
		return dialViaSOCKS(ctx, up, network, addr)
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func (s *Server) dialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	cfg := s.tlsConfigForHost(host)
	if up := s.upstream.Load(); up != nil && strings.EqualFold(up.Scheme, "https") && addr == upstreamProxyAddress(up) {
		cfg.RootCAs = s.upstreamProxyRoots.Load()
		cfg = forceHTTP11TLSConfig(cfg)
	}
	raw, err := s.dialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

func (s *Server) tlsConfigForHost(host string) *tls.Config {
	cfg := &tls.Config{ServerName: host, NextProtos: []string{"h2", "http/1.1"}}
	if s.tr.TLSClientConfig != nil {
		cfg = s.tr.TLSClientConfig.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = host
		}
	}
	return cfg
}

func (s *Server) upstreamProxyTLSConfig(host string) *tls.Config {
	cfg := s.tlsConfigForHost(host)
	cfg.RootCAs = s.upstreamProxyRoots.Load()
	return forceHTTP11TLSConfig(cfg)
}

// SetUpstreamProxy routes outbound traffic through a chained proxy (e.g. a
// corporate proxy). An empty string means connect directly.
func (s *Server) SetUpstreamProxy(raw string) error {
	if strings.TrimSpace(raw) == "" {
		s.upstream.Store(nil)
		s.tr.CloseIdleConnections()
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid upstream proxy URL %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if !isSupportedUpstreamScheme(u.Scheme) {
		return fmt.Errorf("unsupported upstream proxy scheme %q", u.Scheme)
	}
	s.upstream.Store(u)
	s.tr.CloseIdleConnections()
	return nil
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	return (&http.Server{Handler: s}).Serve(ln)
}

// hopHeaders are stripped when forwarding (RFC 7230 §6.1).
var hopHeaders = []string{
	"Proxy-Connection", "Keep-Alive", "Proxy-Authorization",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// hopRequestHeaders are stripped only from outbound requests (not from
// responses relayed to the client). Some hop-by-hop headers — notably
// Connection — must be preserved in MITM responses so Flutter's
// dart:io HttpClient (and similar strict clients) don't treat every
// response as connection-close and time out waiting for data that never
// arrives on a reused connection.
var hopRequestHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authorization",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

func removeHeaders(h http.Header, blacklist []string) {
	for _, k := range blacklist {
		h.Del(k)
	}
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// ServeHTTP dispatches: CONNECT → TLS MITM; absolute-URI → HTTP forward proxy.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	if !r.URL.IsAbs() {
		// Invisible (transparent) proxy mode: a client that isn't configured to
		// use a proxy — e.g. traffic redirected via iptables/pf, DNS spoofing, or
		// port forwarding — sends an origin-form request: "GET /path" with a Host
		// header naming the real target. Reconstruct the absolute URL from the
		// Host header and fall through to normal forwarding. Disabled by default,
		// matching Burp's "Support invisible proxying" option.
		if !s.invisible.Load() {
			http.Error(w, "this is a forward proxy; use an absolute-URI request", http.StatusBadRequest)
			return
		}
		host := r.Host
		if host == "" {
			http.Error(w, "invisible proxy: request has no Host header", http.StatusBadRequest)
			return
		}
		r.URL.Scheme = "http"
		r.URL.Host = host
	}

	scheme := r.URL.Scheme
	host := r.URL.Hostname()
	port := strutil.AtoiOr(r.URL.Port(), defaultPort(scheme))

	flow := buildFlow(r, scheme, host, port, time.Now())
	if isUpgradeRequest(r.Header) {
		s.handleUpgradeHTTP(w, r, flow)
		return
	}
	resp, dropped, err := s.gateAndForward(flow, r)
	switch {
	case dropped:
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		http.Error(w, "request dropped by interseptor", http.StatusBadGateway)
	case err != nil:
		s.fail(w, flow, "upstream: "+err.Error())
	default:
		s.writeResponseHTTP(w, resp, flow)
	}
}

// handleConnect terminates client TLS with a minted leaf, then serves the
// plaintext requests carried over the tunnel, capturing each as an HTTPS flow.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if s.ca == nil {
		http.Error(w, "TLS interception unavailable", http.StatusNotImplemented)
		return
	}
	host, port := splitHostPort(r.Host, 443)
	connectStarted := time.Now()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// TLS-bypass: tunnel this host straight through without MITM, so the client
	// negotiates TLS (and its pinning) with the real origin. Lets a pinned-but-
	// unimportant domain keep working while other domains are still intercepted.
	if s.shouldBypassTLS(host) {
		s.tunnelRaw(clientConn, host, port, r)
		return
	}

	var sni string
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Offer HTTP/2 on the forged leaf; clients that don't select it fall
		// back to HTTP/1.1 and take the ReadRequest loop below (#19).
		NextProtos: h2ALPN,
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := chi.ServerName
			if name == "" {
				name = host // IP literals send no SNI; fall back to the CONNECT host
			}
			sni = chi.ServerName
			return s.ca.LeafForHost(name)
		},
	}
	tlsConn := tls.Server(clientConn, cfg)
	if err := tlsConn.Handshake(); err != nil {
		s.recordTLSFailure(host, port, clientConn.RemoteAddr().String(), r, connectStarted, err)
		// The client rejected our leaf (pinning or untrusted CA). If auto-bypass is
		// on, add this host so the app's next attempt tunnels through and works.
		if s.autoBypass.Load() {
			s.addBypassHost(host)
		}
		return
	}
	defer tlsConn.Close()

	host = connectUpstreamHost(host, sni)

	// Client selected HTTP/2 on the MITM leg — serve it with an http2.Server
	// that bridges each stream into the same forwarding pipeline (#19). When
	// h2 wasn't negotiated, fall through to the HTTP/1.1 request loop.
	if tlsConn.ConnectionState().NegotiatedProtocol == "h2" {
		s.serveH2MITM(tlsConn, host, port)
		return
	}

	br := bufio.NewReader(tlsConn)
	for {
		// Reap tunnels left idle between requests. The deadline is cleared once a
		// request arrives, so in-flight requests, slow bodies, and long-lived
		// WebSocket splices (which are legitimately idle) are never affected.
		tlsConn.SetReadDeadline(time.Now().Add(tunnelIdleTimeout))
		req, err := http.ReadRequest(br)
		if err != nil {
			return // EOF, idle timeout, or malformed → close tunnel
		}
		tlsConn.SetReadDeadline(time.Time{})
		if !s.mitmExchange(tlsConn, br, req, host, port) {
			return
		}
	}
}

// connectUpstreamHost picks which hostname to use for the upstream connection.
//
// Some hardened clients defeat SNI-based interception by resolving the target
// domain themselves and issuing "CONNECT <IP>:443" instead of
// "CONNECT host:443" — the CONNECT target is then a bare IP (a fresh one on
// every DNS lookup for anycast hosts like Cloudflare). Forwarding upstream
// using that IP as the TLS ServerName either sends no SNI at all (crypto/tls
// never sends SNI for IP literals) or the wrong one, so SNI-routed upstreams
// (Cloudflare, most CDNs) reject or misroute the handshake. The client's
// ClientHello SNI is the actual intended hostname, so prefer it for the
// upstream connection whenever the CONNECT target itself was an IP literal —
// this matches how mitmproxy and other transparent MITM proxies behave by
// default. When the CONNECT target is already a domain, it's left untouched.
func connectUpstreamHost(connectHost, sni string) string {
	if sni != "" && net.ParseIP(connectHost) != nil {
		return sni
	}
	return connectHost
}

// tunnelIdleTimeout bounds how long a CONNECT tunnel may sit between requests.
const tunnelIdleTimeout = 3 * time.Minute

// mitmExchange forwards one tunnelled request and writes the response back over
// conn. It returns false when the tunnel should be closed.
func (s *Server) mitmExchange(conn net.Conn, br *bufio.Reader, req *http.Request, host string, port int) bool {
	req.URL.Scheme = "https"
	req.URL.Host = hostPort(host, port, "https")
	flow := buildFlow(req, "https", host, port, time.Now())
	flow.ClientAddr = conn.RemoteAddr().String()

	if isUpgradeRequest(req.Header) {
		s.tunnelUpgrade(conn, br, req, flow)
		return false // the connection is now an opaque upgraded stream
	}

	resp, dropped, err := s.gateAndForward(flow, req)
	if dropped {
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		writeSimpleResponse(conn, http.StatusBadGateway, "request dropped by interseptor")
		return false
	}
	if err != nil {
		flow.Status = http.StatusBadGateway
		flow.Error = "upstream: " + err.Error()
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		writeSimpleResponse(conn, http.StatusBadGateway, err.Error())
		return false
	}

	keepAlive := respKeepAlive(resp)
	if err := s.writeResponseConn(conn, resp, flow); err != nil {
		return false
	}
	if req.Body != nil {
		io.Copy(io.Discard, req.Body) // drain to resync the stream for keep-alive
		req.Body.Close()
	}
	return keepAlive
}

// respKeepAlive reports whether the MITM tunnel can be reused after this response.
// A chunked HTTP/1.1 response has ContentLength −1 but is self-delimiting, so it
// can keep-alive too (the old `ContentLength >= 0` test tore the tunnel down after
// every chunked response, forcing a TLS re-handshake per request).
func respKeepAlive(resp *http.Response) bool {
	if resp.Close {
		return false
	}
	if resp.ContentLength >= 0 {
		return true
	}
	for _, te := range resp.TransferEncoding {
		if strings.EqualFold(te, "chunked") {
			return true
		}
	}
	return false
}

// isUpgradeRequest reports whether r is a protocol-upgrade handshake (e.g.
// WebSocket): a non-empty Upgrade header plus "upgrade" in Connection.
func isUpgradeRequest(h http.Header) bool {
	if h.Get("Upgrade") == "" {
		return false
	}
	for _, v := range h["Connection"] {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// handleUpgradeHTTP hijacks a plain-HTTP client connection and tunnels the
// upgrade through to the upstream.
func (s *Server) handleUpgradeHTTP(w http.ResponseWriter, r *http.Request, flow *store.Flow) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.fail(w, flow, "upgrade: hijacking unsupported")
		return
	}
	clientConn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()
	s.tunnelUpgrade(clientConn, buf.Reader, r, flow)
}

// tunnelUpgrade forwards an upgrade handshake to the upstream verbatim (upgrade
// headers intact, never stripped) and, on a 101, splices bytes bidirectionally
// until either side closes. Frame-level capture (WebSocket messages) is a later
// slice — here we keep the connection working and record the handshake flow.
func (s *Server) tunnelUpgrade(clientConn net.Conn, clientReader *bufio.Reader, r *http.Request, flow *store.Flow) {
	flow.Flags |= store.FlagWebSocket

	up, err := s.dialUpstream(flow.Scheme, flow.Host, flow.Port)
	if err != nil {
		s.recordUpgradeError(clientConn, flow, "upgrade dial: "+err.Error())
		return
	}
	defer up.Close()

	r.RequestURI = "" // required to write a server-received request as a client request
	if err := r.Write(up); err != nil {
		s.recordUpgradeError(clientConn, flow, "upgrade write: "+err.Error())
		return
	}

	upReader := bufio.NewReader(up)
	resp, err := http.ReadResponse(upReader, r)
	if err != nil {
		s.recordUpgradeError(clientConn, flow, "upgrade response: "+err.Error())
		return
	}
	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")
	flow.DurationMs = time.Since(flow.TS).Milliseconds()

	// Relay the handshake head verbatim (do NOT strip hop headers — Connection
	// and Upgrade are the handshake) without consuming the upgraded stream.
	if err := writeResponseHead(clientConn, resp); err != nil {
		resp.Body.Close()
		s.record(flow)
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		_, _ = io.Copy(clientConn, resp.Body)
		resp.Body.Close()
		s.record(flow)
		return
	}
	s.record(flow)

	// Frame-aware relay: capture each WebSocket frame while forwarding verbatim.
	// Each relay closes its write side and signals done from a defer, so a panic
	// in the capture/record path (e.g. a store or notifier callback) can never
	// leak the goroutine or wedge the parent on <-done — the connection is torn
	// down and the wait is released regardless.
	done := make(chan struct{}, 2)
	go s.relayWS(flow.ID, "send", clientReader, up, up, done)
	go s.relayWS(flow.ID, "recv", upReader, clientConn, clientConn, done)
	<-done
	<-done
}

// relayWS runs one direction of the WebSocket splice. It always closes closer
// and sends on done, even if the relay panics — recovering here keeps a capture
// bug from crashing the whole proxy or hanging tunnelUpgrade.
func (s *Server) relayWS(flowID int64, dir string, src *bufio.Reader, dst net.Conn, closer net.Conn, done chan<- struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("proxy: ws relay %s panic: %v", dir, r)
		}
		closer.Close()
		done <- struct{}{}
	}()
	s.relayWSFrames(flowID, dir, src, dst)
}

func (s *Server) recordUpgradeError(clientConn net.Conn, flow *store.Flow, msg string) {
	flow.Status = http.StatusBadGateway
	flow.Error = msg
	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.record(flow)
	writeSimpleResponse(clientConn, http.StatusBadGateway, msg)
}

// dialUpstream opens a raw connection to the target, using TLS for https so the
// upgraded stream is end-to-end encrypted to the origin.
func (s *Server) dialUpstream(scheme, host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	// KeepAlive lets the OS detect a half-open upstream (peer vanished without
	// FIN/RST) so an idle-but-dead WebSocket tunnel is eventually torn down,
	// without an application-level timeout that would kill a legitimately idle one.
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	tlsCfg := func() *tls.Config {
		return s.tlsConfigForHost(host)
	}
	// If a chained upstream proxy is configured, CONNECT-tunnel through it.
	// (Plain HTTP/HTTPS requests already honor it via s.tr.Proxy; a WebSocket
	// upgrade dials here and would otherwise bypass the upstream.)
	if up := s.upstream.Load(); up != nil {
		var (
			raw net.Conn
			err error
		)
		switch {
		case isHTTPUpstream(up):
			raw, err = dialViaUpstream(d, up, addr, s.upstreamProxyTLSConfig(up.Hostname()))
		case isSOCKSUpstream(up):
			raw, err = dialViaSOCKS(context.Background(), up, "tcp", addr)
		default:
			return nil, fmt.Errorf("unsupported upstream proxy scheme %q", up.Scheme)
		}
		if err != nil {
			return nil, err
		}
		if scheme == "https" {
			tc := tls.Client(raw, forceHTTP11TLSConfig(tlsCfg()))
			_ = raw.SetDeadline(time.Now().Add(d.Timeout))
			if err := tc.Handshake(); err != nil {
				raw.Close()
				return nil, err
			}
			_ = raw.SetDeadline(time.Time{})
			return tc, nil
		}
		return raw, nil
	}
	// Direct (no upstream) — unchanged.
	if scheme == "https" {
		return tls.DialWithDialer(d, "tcp", addr, forceHTTP11TLSConfig(tlsCfg()))
	}
	return d.Dial("tcp", addr)
}

func forceHTTP11TLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{NextProtos: []string{"http/1.1"}}
	}
	c := cfg.Clone()
	c.NextProtos = []string{"http/1.1"}
	return c
}

// dialViaUpstream opens a TCP tunnel to addr through an HTTP CONNECT proxy.
func dialViaUpstream(d *net.Dialer, up *url.URL, addr string, tlsConfig *tls.Config) (net.Conn, error) {
	deadline := dialerDeadline(d)
	conn, err := d.Dial("tcp", upstreamProxyAddress(up))
	if err != nil {
		return nil, err
	}
	if !deadline.IsZero() {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if strings.EqualFold(up.Scheme, "https") {
		cfg := &tls.Config{ServerName: up.Hostname()}
		if tlsConfig != nil {
			cfg = tlsConfig.Clone()
			cfg.ServerName = up.Hostname()
		}
		cfg = forceHTTP11TLSConfig(cfg)
		tc := tls.Client(conn, cfg)
		if err := tc.Handshake(); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
	if up.User != nil {
		if pw, ok := up.User.Password(); ok {
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(up.User.Username()+":"+pw)))
		}
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("upstream CONNECT %s: %s", addr, resp.Status)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func dialerDeadline(d *net.Dialer) time.Time {
	deadline := d.Deadline
	if d.Timeout > 0 {
		timeoutDeadline := time.Now().Add(d.Timeout)
		if deadline.IsZero() || timeoutDeadline.Before(deadline) {
			deadline = timeoutDeadline
		}
	}
	return deadline
}

func upstreamProxyAddress(up *url.URL) string {
	port := up.Port()
	if port == "" {
		port = strconv.Itoa(defaultPort(strings.ToLower(up.Scheme)))
	}
	return net.JoinHostPort(up.Hostname(), port)
}

func isHTTPUpstream(up *url.URL) bool {
	return up.Scheme == "http" || up.Scheme == "https"
}

func isSOCKSUpstream(up *url.URL) bool {
	return up.Scheme == "socks5" || up.Scheme == "socks5h"
}

func isSupportedUpstreamScheme(scheme string) bool {
	return scheme == "http" || scheme == "https" || scheme == "socks5" || scheme == "socks5h"
}

func dialViaSOCKS(ctx context.Context, up *url.URL, network, addr string) (net.Conn, error) {
	if up.Scheme == "socks5" {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("resolve SOCKS destination %q: no addresses", host)
		}
		addr = net.JoinHostPort(ips[0].IP.String(), port)
	}
	var auth *proxy.Auth
	if up.User != nil {
		password, _ := up.User.Password()
		auth = &proxy.Auth{User: up.User.Username(), Password: password}
	}
	dialer, err := proxy.SOCKS5(network, upstreamProxyAddress(up), auth, &net.Dialer{})
	if err != nil {
		return nil, err
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS dialer does not support context")
	}
	return contextDialer.DialContext(ctx, network, addr)
}

// writeResponseHead writes a response's status line and headers (no body) so an
// upgrade handshake can be relayed without reading the upgraded stream.
func writeResponseHead(w io.Writer, resp *http.Response) error {
	status := resp.Status
	if status == "" {
		status = strconv.Itoa(resp.StatusCode) + " " + http.StatusText(resp.StatusCode)
	}
	if _, err := fmt.Fprintf(w, "HTTP/1.1 %s\r\n", status); err != nil {
		return err
	}
	if err := resp.Header.Write(w); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

// gateAndForward runs the intercept gate + match-&-replace, tees the request
// body, forwards upstream, and finalizes the request side of flow. It returns
// (resp, false, nil) on success, (nil, true, nil) if the request was dropped,
// or (nil, false, err) on an upstream error.
func (s *Server) gateAndForward(flow *store.Flow, r *http.Request) (*http.Response, bool, error) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = flow.Scheme
	out.URL.Host = hostPort(flow.Host, flow.Port, flow.Scheme)
	removeHeaders(out.Header, hopRequestHeaders)

	// Intercept gate (Burp-style hold) — only for in-scope, non-self, non-telemetry requests.
	if s.eng != nil && s.eng.Enabled() && s.shouldCapture(flow) && (s.Scope == nil || s.Scope.InScope(flow)) &&
		(!s.suppressTelemetry.Load() || !isBrowserTelemetry(flow.Host)) &&
		(!s.suppressAndroidTelemetry.Load() || !isAndroidTelemetry(flow.Host)) {
		raw, truncated := dumpRequest(out)
		if truncated {
			// The body exceeds the editor buffer, so the raw dump is truncated and a
			// round-tripped edit would forward a truncated body. Skip the hold and
			// forward the full original stream unedited rather than silently corrupt
			// it — a >64 MB body can't be meaningfully hand-edited anyway.
			log.Printf("proxy: intercept bypassed for %s %s%s — body too large to edit (> %d bytes)",
				out.Method, flow.Host, flow.Path, maxTransformBody)
		} else {
			d := s.eng.HoldContext(out.Context(), flow, out, raw)
			// Only flag as intercepted when the request was actually held; the
			// conditional filter forwards non-matching requests without holding.
			if d.Held {
				flow.Flags |= store.FlagIntercepted
			}
			if d.Drop {
				flow.Flags |= store.FlagDropped
				return nil, true, nil
			}
			out = d.Request
			out.RequestURI = ""
			out.URL.Scheme = flow.Scheme
			if d.Edited {
				if flow.OriginalReqHeaders == nil {
					flow.OriginalReqHeaders = cloneHeaders(flow.ReqHeaders)
				}
				if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
					flow.OriginalReqBodyHash, _ = s.storeBytes(raw[i+4:])
				}
				flow.Flags |= store.FlagEdited

				// An edited Host header must actually retarget the connection —
				// not just the wire header — or the operator gets a confused-deputy
				// primitive: connect to the original host while claiming (on the
				// wire) to be talking to the edited one. parseEditedRequest already
				// derives out.URL.Host from the edited Host text (falling back to
				// the original when Host wasn't touched), so trust it here and
				// bring flow.Host/Port along so dialing, the wire header, and the
				// recorded history all agree on where the request actually went.
				if h, p, ok := splitHostPortOK(out.URL.Host); ok {
					if p == 0 {
						p = defaultPort(flow.Scheme) // edited Host had no explicit port
					}
					flow.Host, flow.Port = h, p
				}
			}
			out.URL.Host = hostPort(flow.Host, flow.Port, flow.Scheme)
			out.Host = out.URL.Host

			// Refuse to dial Interseptor's own loopback listeners (control plane or
			// proxy) once the edit is resolved to its FINAL target — this must run
			// after the Host retarget above, not before, or an edited Host would slip
			// past it entirely. Without this, an MCP-driving AI agent (or
			// prompt-injected content reaching one) could hold a request, edit its
			// Host to 127.0.0.1:<control-port>, and forward a crafted control-API
			// request (e.g. GET /api/keys); the resulting connection is genuinely
			// loopback-sourced with a loopback Host header, so the control API's
			// unauthenticated-loopback trust path (internal/control/guard.go) would
			// grant it full access. Repeater/Intruder/WS-repeater/the AI agent tool
			// already refuse this exact class of self-targeting via
			// targetsOwnListener/isOwnListener; this mirrors that guard using the
			// same SelfPorts loopback-port set the proxy already keeps in sync.
			if s.isOwnListenerTarget(flow.Host, flow.Port) {
				return nil, false, fmt.Errorf("refusing to forward to Interseptor's own listener")
			}
		}
	}

	// Match & replace (request-side) — skip our own traffic.
	if s.eng != nil && s.shouldCapture(flow) {
		if err := s.eng.ApplyRules(out); err != nil {
			return nil, false, fmt.Errorf("apply rules: %w", err)
		}
	}

	// Record what is actually being sent (post edit/rules) and surface the
	// in-flight request in history now, before we know the response. record()
	// later updates this same row once the response (or error) is known.
	flow.Method = out.Method
	flow.Path = out.URL.RequestURI()
	flow.ReqHeaders = headerWithHost(out)
	s.recordRequest(flow)

	// Tee the request body to the store; capture failure must not break forwarding.
	var reqFinalize func() (string, int64, error)
	if reqTee, fin, err := s.teeBody(flow, out.Body); err != nil {
		flow.Flags |= store.FlagCaptureError
	} else if reqTee != nil {
		out.Body = io.NopCloser(reqTee)
		reqFinalize = fin
	}

	resp, rtErr := s.tr.RoundTrip(out)

	if reqFinalize != nil {
		h, n, ferr := reqFinalize()
		flow.ReqBodyHash, flow.ReqLen = h, n
		if ferr != nil {
			flow.Flags |= store.FlagCaptureError
		}
	}

	if rtErr != nil {
		return nil, false, rtErr
	}
	if resp.StatusCode == http.StatusProxyAuthRequired && s.upstream.Load() != nil {
		// Go's Transport does not expose whether a 407 came from chained proxy or
		// origin. Treat every 407 with chaining enabled as proxy auth failure: HTTP
		// origins should use 401, and forwarding Proxy-Authenticate triggers prompts.
		const maxProxyErrorBody = 1 << 20
		_, _ = io.CopyN(io.Discard, resp.Body, maxProxyErrorBody)
		_ = resp.Body.Close()
		return nil, false, fmt.Errorf("upstream proxy authentication required")
	}
	return resp, false, nil
}

// writeResponseHTTP streams the upstream response to an http.ResponseWriter
// while tee'ing the body to the store, then records the flow.
func (s *Server) writeResponseHTTP(w http.ResponseWriter, resp *http.Response, flow *store.Flow) {
	defer resp.Body.Close()
	if st, hdr, body, transformed, dropped := s.maybeInterceptResponse(flow, resp); dropped {
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		http.Error(w, "response dropped by interseptor", http.StatusBadGateway)
		return
	} else if transformed {
		copyHeader(w.Header(), hdr)
		w.WriteHeader(st)
		w.Write(body)
		flow.Status, flow.ResHeaders, flow.Mime = st, hdr.Clone(), hdr.Get("Content-Type")
		flow.ResBodyHash, flow.ResLen = s.storeBytes(body)
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		return
	}
	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")
	removeHopHeaders(resp.Header)
	if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 {
		resp.TransferEncoding = []string{"chunked"}
	}
	copyHeader(w.Header(), resp.Header)
	if len(resp.Trailer) > 0 {
		// Declare announced trailer keys before the body so the server emits them.
		tk := make([]string, 0, len(resp.Trailer))
		for k := range resp.Trailer {
			tk = append(tk, k)
		}
		w.Header().Set("Trailer", strings.Join(tk, ", "))
	}
	w.WriteHeader(resp.StatusCode)

	if resTee, resFinalize, err := s.teeBody(flow, resp.Body); err == nil && resTee != nil {
		_, cerr := io.Copy(w, resTee)
		h, n, _ := resFinalize()
		flow.ResBodyHash, flow.ResLen = h, n
		if cerr != nil {
			// Client aborted mid-stream: the stored body is whatever was
			// tee'd before the write failed. Flag it so history/replay don't
			// treat a truncated body + its length/hash as the full response.
			flow.Flags |= store.FlagCaptureError
			flow.Error = "stream resp: " + cerr.Error()
		}
	} else if err != nil {
		flow.Flags |= store.FlagCaptureError
		flow.Error = "capture resp: " + err.Error()
	}
	// Now that the body is fully read, resp.Trailer holds the trailer values —
	// forward them (the plain-HTTP path previously dropped them).
	for k, vv := range resp.Trailer {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.record(flow)
}

// writeResponseConn serializes the upstream response onto a raw conn (the MITM
// path) while tee'ing the body to the store, then records the flow.
func (s *Server) writeResponseConn(conn net.Conn, resp *http.Response, flow *store.Flow) error {
	upstream := resp.Body
	defer upstream.Close()
	if st, hdr, body, transformed, dropped := s.maybeInterceptResponse(flow, resp); dropped {
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		s.record(flow)
		writeSimpleResponse(conn, http.StatusBadGateway, "response dropped by interseptor")
		return nil
	} else if transformed {
		flow.Status, flow.ResHeaders, flow.Mime = st, hdr.Clone(), hdr.Get("Content-Type")
		flow.ResBodyHash, flow.ResLen = s.storeBytes(body)
		flow.DurationMs = time.Since(flow.TS).Milliseconds()
		_, werr := conn.Write(buildRawResponse(st, hdr, body))
		s.record(flow)
		return werr
	}
	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")

	removeHopHeaders(resp.Header)

	// Record the origin protocol before any framing rewrite so History shows
	// whether upstream spoke HTTP/2 even though we speak HTTP/1.1 to the client.
	if resp.Proto != "" {
		flow.HTTPVersion = resp.Proto
	}

	// Go's http.Transport may produce an HTTP/2 response. When written
	// back over an HTTP/1.1 MITM connection, resp.Write sends
	// "HTTP/2.0 ..." as the status line, ContentLength=-1, and empty
	// TransferEncoding — so the client gets no framing at all and
	// hangs until timeout.  Fix: downgrade to HTTP/1.1 with chunked
	// framing when necessary. Full client↔proxy h2 MITM is tracked in #19.
	if resp.ProtoMajor >= 2 {
		resp.ProtoMajor, resp.ProtoMinor = 1, 1
		resp.Proto = "HTTP/1.1"
	}
	if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 {
		resp.TransferEncoding = []string{"chunked"}
	}

	resTee, resFinalize, err := s.teeBody(flow, upstream)
	if err == nil && resTee != nil {
		resp.Body = io.NopCloser(resTee)
	} else if err != nil {
		flow.Flags |= store.FlagCaptureError
		flow.Error = "capture resp: " + err.Error()
		resFinalize = func() (string, int64, error) { return "", 0, nil }
	}

	werr := resp.Write(conn)
	h, n, _ := resFinalize()
	flow.ResBodyHash, flow.ResLen = h, n
	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.record(flow)
	return werr
}

// maxTransformBody bounds how much of a request/response body is buffered in
// memory to transform (match-&-replace) or hold for interception. Bodies over the
// cap are forwarded untransformed/streamed rather than buffered unbounded.
const maxTransformBody = 64 << 20

// restoreBody re-wraps a partially-read body so the unread remainder still streams
// (prefix already read + the rest) and Close still closes the original reader.
func restoreBody(prefix []byte, rest io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), rest), rest}
}

// maybeInterceptResponse applies response-side match-&-replace and the response
// hold gate when active. It returns the final status/header/body to send. When
// neither rules nor response-interception apply, transformed is false and the
// caller streams the original response untouched (no buffering).
func (s *Server) maybeInterceptResponse(flow *store.Flow, resp *http.Response) (status int, header http.Header, body []byte, transformed, dropped bool) {
	if s.eng == nil || !s.shouldCapture(flow) {
		return 0, nil, nil, false, false // our own traffic is forwarded untouched
	}
	hasRules := s.eng.HasResponseRules()
	hold := s.eng.ResponseEnabled() && (s.Scope == nil || s.Scope.InScope(flow))
	if !hasRules && !hold {
		return 0, nil, nil, false, false
	}

	lr := io.LimitReader(resp.Body, maxTransformBody+1)
	b, rerr := io.ReadAll(lr)
	if rerr != nil || int64(len(b)) > maxTransformBody {
		// Too large to buffer/transform (or a mid-body read error) — restore the
		// stream and forward it untransformed, rather than buffering unbounded or
		// forwarding a silently-truncated body. Rules/hold don't apply over the cap.
		resp.Body = restoreBody(b, resp.Body)
		return 0, nil, nil, false, false
	}
	h := resp.Header.Clone()
	removeHopHeaders(h)
	st := resp.StatusCode
	originalBody := append([]byte(nil), b...)
	originalHeaders := h.Clone()
	if hasRules {
		h, b = s.eng.ApplyResponseRules(h, b)
		if !bytes.Equal(originalBody, b) {
			flow.OriginalResHeaders = cloneHeaders(originalHeaders)
			flow.OriginalResBodyHash, _ = s.storeBytes(originalBody)
		}
	}
	if hold {
		flow.Flags |= store.FlagIntercepted
		ctx := context.Background()
		if resp.Request != nil {
			ctx = resp.Request.Context()
		}
		d := s.eng.HoldResponseContext(ctx, flow, buildRawResponse(st, h, b))
		if d.Drop {
			flow.Flags |= store.FlagDropped
			return 0, nil, nil, false, true
		}
		if d.Edited {
			if nst, nh, nb, err := parseRawResponse(d.Raw); err == nil {
				if flow.OriginalResHeaders == nil {
					flow.OriginalResHeaders = cloneHeaders(flow.ResHeaders)
				}
				st, h, b = nst, nh, nb
				flow.Flags |= store.FlagResponseEdited
			}
		}
	}
	// Keep framing consistent with the (possibly edited) body.
	h.Set("Content-Length", strconv.Itoa(len(b)))
	h.Del("Transfer-Encoding")
	return st, h, b, true, false
}

func cloneHeaders(src http.Header) http.Header {
	if src == nil {
		return nil
	}
	return src.Clone()
}

// storeBytes captures an in-memory body into the content-addressed store.
func (s *Server) storeBytes(b []byte) (string, int64) {
	if len(b) == 0 {
		return "", 0
	}
	tee, finalize, err := s.cap.TeeBody(bytes.NewReader(b))
	if err != nil || tee == nil {
		return "", 0
	}
	io.Copy(io.Discard, tee)
	h, n, _ := finalize()
	return h, n
}

func buildRawResponse(status int, h http.Header, body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}

// parseRawResponse parses an (edited) raw response: status line + headers via
// http.ReadResponse, body taken as everything after the blank line.
func parseRawResponse(raw []byte) (int, http.Header, []byte, error) {
	norm := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", "\r\n")
	head, body := norm, ""
	if i := strings.Index(norm, "\r\n\r\n"); i >= 0 {
		head = norm[:i] + "\r\n\r\n"
		body = norm[i+4:]
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(head)), nil)
	if err != nil {
		return 0, nil, nil, err
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header, []byte(body), nil
}

// fail records an errored flow and writes a 502 to the client. Used only before
// any response header has been written.
func (s *Server) fail(w http.ResponseWriter, flow *store.Flow, msg string) {
	flow.Status = http.StatusBadGateway
	flow.Error = msg
	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.record(flow)
	http.Error(w, msg, http.StatusBadGateway)
}

// record persists a flow and notifies the events sink.
// shouldCapture reports whether a flow should be persisted. Traffic to our own
// loopback listeners (control plane, proxy) is forwarded but never recorded.
func (s *Server) shouldCapture(flow *store.Flow) bool {
	if len(s.SelfPorts) == 0 || flow == nil || !isLoopbackName(flow.Host) {
		return true
	}
	for _, p := range s.SelfPorts {
		if p == flow.Port {
			return false
		}
	}
	return true
}

// isOwnListenerTarget reports whether host:port names one of Interseptor's own
// loopback listeners (control plane or proxy) — the same SelfPorts set
// shouldCapture uses to keep our own UI/API traffic out of history. Mirrors
// internal/control's targetsOwnListener/isOwnListener (loopback-normalized:
// 127.x / ::1 / localhost, not a literal string match), which Repeater,
// Intruder, WS-repeater, and the AI agent tool already use to refuse being
// coerced into attacking Interseptor's own control API. Used by
// gateAndForward after Host-retargeting is resolved, so an edited Host can't
// slip a forward past this check.
func (s *Server) isOwnListenerTarget(host string, port int) bool {
	if len(s.SelfPorts) == 0 || !isLoopbackName(host) {
		return false
	}
	for _, p := range s.SelfPorts {
		if p == port {
			return true
		}
	}
	return false
}

// SetCaptureScopeOnly switches between persisting all traffic (false) and only
// in-scope traffic (true) — a space optimization for long engagements. With no
// scope rules defined everything is in scope, so this becomes a no-op until the
// operator sets a target scope.
func (s *Server) SetCaptureScopeOnly(v bool) { s.scopeOnly.Store(v) }

// SetSuppressBrowserTelemetry controls whether known Chrome and Firefox
// background telemetry, update, and crash-reporting hosts are silently
// forwarded without being captured or held by the intercept gate. Enabled by
// default; users may turn it off to inspect browser background traffic.
func (s *Server) SetSuppressBrowserTelemetry(v bool) { s.suppressTelemetry.Store(v) }

// SetSuppressAndroidTelemetry controls whether known Android OS, Google Play
// Services, Crashlytics, and Analytics phone-home hosts are silently forwarded
// without being captured or held by the intercept gate. Enabled by default;
// users may turn it off to inspect GMS / SDK background traffic.
func (s *Server) SetSuppressAndroidTelemetry(v bool) { s.suppressAndroidTelemetry.Store(v) }

// SetInvisibleProxy toggles transparent/invisible proxy mode (Burp's "Support
// invisible proxying"). When enabled, origin-form requests from clients that
// aren't proxy-configured (traffic redirected via iptables/pf/DNS/port
// forwarding) are forwarded to the host named in their Host header, instead of
// being rejected as malformed proxy requests. Absolute-URI and CONNECT requests
// keep working unchanged.
func (s *Server) SetInvisibleProxy(v bool) { s.invisible.Store(v) }

// persistable reports whether a flow should be written to history: never our own
// loopback traffic; never browser/Android telemetry when suppression is on; and
// — when scope-only capture is on and a scope is set — only when it is in scope.
func (s *Server) persistable(flow *store.Flow) bool {
	if !s.shouldCapture(flow) {
		return false
	}
	if s.suppressTelemetry.Load() && isBrowserTelemetry(flow.Host) {
		return false
	}
	if s.suppressAndroidTelemetry.Load() && isAndroidTelemetry(flow.Host) {
		return false
	}
	if s.scopeOnly.Load() && s.Scope != nil && !s.Scope.InScope(flow) {
		return false
	}
	return true
}

// teeBody captures a body to the content-addressed store and returns a reader to
// forward plus a finalize() yielding (hash, len) — mirroring capture.TeeBody. When
// the flow is not persistable (scope-only mode, out of scope) it skips storage
// entirely and streams the body straight through, so out-of-scope bodies (the
// bulk of disk use) never land on disk.
func (s *Server) teeBody(flow *store.Flow, body io.Reader) (io.Reader, func() (string, int64, error), error) {
	if s.persistable(flow) {
		return s.cap.TeeBody(body)
	}
	return body, func() (string, int64, error) { return "", 0, nil }, nil
}

// isLoopbackName reports whether a bare hostname names the loopback interface.
func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// recordRequest inserts a flow the moment its request is sent upstream — before
// the response is known — so it shows in history immediately. Idempotent: it is
// a no-op once the flow has an ID. record() later fills in the response.
func (s *Server) recordRequest(flow *store.Flow) {
	if flow.ID != 0 || !s.persistable(flow) {
		return
	}
	if _, err := s.st.InsertFlow(flow); err != nil {
		log.Printf("proxy: insert flow %s %s%s: %v", flow.Method, flow.Host, flow.Path, err)
		return
	}
	s.cap.TagIfAuth(flow.ID, flow.Path) // best-effort auth surface tagging
	if s.events != nil {
		s.events.FlowCaptured(flow)
	}
}

// record persists a flow's final state. If it was already inserted at request
// time (recordRequest), this updates that row in place and emits a flow.update;
// otherwise — e.g. a request dropped before it was ever sent — it inserts and
// emits flow.new.
func (s *Server) record(flow *store.Flow) {
	if flow.ID != 0 {
		// Already inserted at request time — always finish it (the scope decision
		// was made then) so an in-flight scope change can't strand a half-flow.
		if err := s.st.UpdateFlow(flow); err != nil {
			log.Printf("proxy: update flow %d (%s %s%s): %v", flow.ID, flow.Method, flow.Host, flow.Path, err)
			return
		}
		if s.events != nil {
			s.events.FlowUpdated(flow)
		}
		plugin.EmitFlowCaptured(flow.ID) // extension hooks (best-effort, off the wire)
		return
	}
	if !s.persistable(flow) {
		return
	}
	if _, err := s.st.InsertFlow(flow); err != nil {
		log.Printf("proxy: persist flow %s %s%s: %v", flow.Method, flow.Host, flow.Path, err)
		return
	}
	s.cap.TagIfAuth(flow.ID, flow.Path) // best-effort auth surface tagging
	if s.events != nil {
		s.events.FlowCaptured(flow)
	}
	plugin.EmitFlowCaptured(flow.ID) // extension hooks (best-effort, off the wire)
}

// buildFlow constructs the request-side flow metadata from an inbound request.
func buildFlow(r *http.Request, scheme, host string, port int, start time.Time) *store.Flow {
	return &store.Flow{
		TS:          start,
		Method:      r.Method,
		Scheme:      scheme,
		Host:        host,
		Port:        port,
		Path:        r.URL.RequestURI(),
		HTTPVersion: r.Proto,
		ClientAddr:  r.RemoteAddr,
		ReqHeaders:  headerWithHost(r),
	}
}

// headerWithHost clones a request's headers, folding in the Host (which Go
// keeps off the Header map).
func headerWithHost(r *http.Request) map[string][]string {
	h := r.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if r.Host != "" {
		h.Set("Host", r.Host)
	}
	return h
}

// dumpRequest renders an origin-form raw request (request line + headers + body)
// for the intercept UI to edit. It reads and restores the body. truncated is true
// when the body exceeded the editor buffer, in which case the returned dump holds
// only the first maxTransformBody bytes (the full stream is restored to r.Body).
func dumpRequest(r *http.Request) (raw []byte, truncated bool) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxTransformBody+1))
		if int64(len(body)) > maxTransformBody {
			// Body too large to buffer for the intercept editor — restore the full
			// stream so forwarding isn't broken; the editable dump is truncated.
			r.Body = restoreBody(body, r.Body)
			truncated = true
		} else {
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s %s\r\n", r.Method, r.URL.RequestURI(), r.Proto)
	if r.Host != "" {
		fmt.Fprintf(&b, "Host: %s\r\n", r.Host)
	}
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range r.Header[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes(), truncated
}

func writeSimpleResponse(conn net.Conn, code int, msg string) {
	body := msg + "\n"
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func defaultPort(scheme string) int {
	switch scheme {
	case "https":
		return 443
	case "socks5", "socks5h":
		return 1080
	default:
		return 80
	}
}

func hostPort(host string, port int, scheme string) string {
	if port == defaultPort(scheme) {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func splitHostPort(hostport string, def int) (string, int) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, def
	}
	return h, strutil.AtoiOr(p, def)
}

// splitHostPortOK parses a URL host[:port] string (as produced by
// parseEditedRequest from an edited Host header) into a bare host and port.
// ok is false only when hostport is empty. A bare host with no ":port" suffix
// is a valid result (it just means "use the request's scheme default port"):
// port comes back 0 and the caller is responsible for supplying that default,
// since only the caller knows the scheme.
func splitHostPortOK(hostport string) (host string, port int, ok bool) {
	if hostport == "" {
		return "", 0, false
	}
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		return h, strutil.AtoiOr(p, 0), true
	}
	// No ":port" suffix — bare host, port left for the caller to default.
	return hostport, 0, true
}
