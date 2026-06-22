// Package proxy implements an intercepting HTTP/HTTPS forward proxy that
// captures every flow. Plain HTTP is forwarded directly; HTTPS is intercepted
// via CONNECT + a locally-minted leaf certificate (TLS MITM).
package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/tlsca"
)

// Events is an optional sink notified when a flow is captured (used by the
// control plane to push live updates to the UI).
type Events interface {
	FlowCaptured(*store.Flow)
}

// Server is the intercepting forward-proxy HTTP handler.
type Server struct {
	st     *store.Store
	cap    *capture.Capturer
	ca     *tlsca.CA         // nil → HTTPS CONNECT returns 501
	eng    *intercept.Engine // nil → no intercept/rules
	events Events            // nil → no live notifications
	tr     *http.Transport
}

// New builds a proxy Server. ca, eng, and events may each be nil.
func New(st *store.Store, cap *capture.Capturer, ca *tlsca.CA, eng *intercept.Engine, events Events) *Server {
	return &Server{
		st:     st,
		cap:    cap,
		ca:     ca,
		eng:    eng,
		events: events,
		tr: &http.Transport{
			Proxy:                 nil, // dial upstream directly
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	return (&http.Server{Handler: s}).Serve(ln)
}

// hopHeaders are stripped when forwarding (RFC 7230 §6.1).
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
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
		http.Error(w, "this is a forward proxy; use an absolute-URI request", http.StatusBadRequest)
		return
	}

	scheme := r.URL.Scheme
	host := r.URL.Hostname()
	port := atoiOr(r.URL.Port(), defaultPort(scheme))

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
		http.Error(w, "request dropped by interceptor", http.StatusBadGateway)
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

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := chi.ServerName
			if name == "" {
				name = host // IP literals send no SNI; fall back to the CONNECT host
			}
			return s.ca.LeafForHost(name)
		},
	}
	tlsConn := tls.Server(clientConn, cfg)
	if err := tlsConn.Handshake(); err != nil {
		return // client likely rejected our leaf (pinning)
	}
	defer tlsConn.Close()

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return // EOF or malformed → close tunnel
		}
		if !s.mitmExchange(tlsConn, br, req, host, port) {
			return
		}
	}
}

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
		writeSimpleResponse(conn, http.StatusBadGateway, "request dropped by interceptor")
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

	keepAlive := resp.ContentLength >= 0 && !resp.Close
	if err := s.writeResponseConn(conn, resp, flow); err != nil {
		return false
	}
	if req.Body != nil {
		io.Copy(io.Discard, req.Body) // drain to resync the stream for keep-alive
		req.Body.Close()
	}
	return keepAlive
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
		s.record(flow)
		return
	}
	s.record(flow)

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return // upstream declined the upgrade; nothing to splice
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(up, clientReader); up.Close(); done <- struct{}{} }()
	go func() { io.Copy(clientConn, upReader); clientConn.Close(); done <- struct{}{} }()
	<-done
	<-done
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
	d := &net.Dialer{Timeout: 30 * time.Second}
	if scheme == "https" {
		cfg := &tls.Config{ServerName: host}
		if s.tr.TLSClientConfig != nil {
			cfg = s.tr.TLSClientConfig.Clone()
			if cfg.ServerName == "" {
				cfg.ServerName = host
			}
		}
		return tls.DialWithDialer(d, "tcp", addr, cfg)
	}
	return d.Dial("tcp", addr)
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
	removeHopHeaders(out.Header)

	// Intercept gate (Burp-style hold).
	if s.eng != nil && s.eng.Enabled() {
		raw := dumpRequest(out)
		flow.Flags |= store.FlagIntercepted
		d := s.eng.Hold(flow, out, raw)
		if d.Drop {
			flow.Flags |= store.FlagDropped
			return nil, true, nil
		}
		out = d.Request
		out.RequestURI = ""
		out.URL.Scheme = flow.Scheme
		out.URL.Host = hostPort(flow.Host, flow.Port, flow.Scheme)
		if d.Edited {
			flow.Flags |= store.FlagEdited
		}
	}

	// Match & replace (request-side).
	if s.eng != nil {
		if err := s.eng.ApplyRules(out); err != nil {
			return nil, false, fmt.Errorf("apply rules: %w", err)
		}
	}

	// Tee the request body to the store; capture failure must not break forwarding.
	var reqFinalize func() (string, int64, error)
	if reqTee, fin, err := s.cap.TeeBody(out.Body); err != nil {
		flow.Flags |= store.FlagCaptureError
	} else if reqTee != nil {
		out.Body = io.NopCloser(reqTee)
		reqFinalize = fin
	}

	resp, rtErr := s.tr.RoundTrip(out)

	if reqFinalize != nil {
		h, n, _ := reqFinalize()
		flow.ReqBodyHash, flow.ReqLen = h, n
	}
	// Record what was actually sent (post edit/rules).
	flow.Method = out.Method
	flow.Path = out.URL.RequestURI()
	flow.ReqHeaders = headerWithHost(out)

	if rtErr != nil {
		return nil, false, rtErr
	}
	return resp, false, nil
}

// writeResponseHTTP streams the upstream response to an http.ResponseWriter
// while tee'ing the body to the store, then records the flow.
func (s *Server) writeResponseHTTP(w http.ResponseWriter, resp *http.Response, flow *store.Flow) {
	defer resp.Body.Close()
	removeHopHeaders(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")

	if resTee, resFinalize, err := s.cap.TeeBody(resp.Body); err == nil && resTee != nil {
		if _, err := io.Copy(w, resTee); err != nil {
			flow.Error = "stream resp: " + err.Error()
		}
		h, n, _ := resFinalize()
		flow.ResBodyHash, flow.ResLen = h, n
	} else if err != nil {
		flow.Flags |= store.FlagCaptureError
		flow.Error = "capture resp: " + err.Error()
	}

	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.record(flow)
}

// writeResponseConn serializes the upstream response onto a raw conn (the MITM
// path) while tee'ing the body to the store, then records the flow.
func (s *Server) writeResponseConn(conn net.Conn, resp *http.Response, flow *store.Flow) error {
	upstream := resp.Body
	defer upstream.Close()
	removeHopHeaders(resp.Header)

	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")

	resTee, resFinalize, err := s.cap.TeeBody(upstream)
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
func (s *Server) record(flow *store.Flow) {
	if _, err := s.st.InsertFlow(flow); err != nil {
		log.Printf("proxy: persist flow %s %s%s: %v", flow.Method, flow.Host, flow.Path, err)
		return
	}
	if s.events != nil {
		s.events.FlowCaptured(flow)
	}
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
// for the intercept UI to edit. It reads and restores the body.
func dumpRequest(r *http.Request) []byte {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
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
	return b.Bytes()
}

func writeSimpleResponse(conn net.Conn, code int, msg string) {
	body := msg + "\n"
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func defaultPort(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
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
	return h, atoiOr(p, def)
}
