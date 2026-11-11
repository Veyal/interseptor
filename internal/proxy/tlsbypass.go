package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Veyal/interseptor/internal/hostpattern"
	"github.com/Veyal/interseptor/internal/store"
)

// SetTLSBypassHosts replaces the set of host patterns that are tunneled raw
// (no MITM). Patterns are exact or "*.wildcard" (see hostpattern). Entries are
// trimmed, lower-cased, de-duplicated; blanks are dropped.
func (s *Server) SetTLSBypassHosts(hosts []string) {
	s.bypassMu.Lock()
	s.bypassHosts.Store(normalizeHosts(hosts))
	s.bypassVersion.Add(1)
	s.bypassMu.Unlock()
}

// TLSBypassHosts returns the current bypass patterns (a copy).
func (s *Server) TLSBypassHosts() []string {
	return copyHosts(s.bypassHosts.Load())
}

// SetOriginTLSVerify controls certificate verification for origin TLS only.
// Existing active connections continue; idle connections are closed so new
// requests use updated policy.
func (s *Server) SetOriginTLSVerify(v bool) {
	s.originTLSVerify.Store(v)
	s.tr.CloseIdleConnections()
}

// OriginTLSVerify reports whether origin certificate verification is enabled.
func (s *Server) OriginTLSVerify() bool { return s.originTLSVerify.Load() }

// SetOriginTLSVerifyBypassHosts replaces origin host patterns permitted to skip
// certificate verification. This affects only origin TLS, never HTTPS upstream proxies.
func (s *Server) SetOriginTLSVerifyBypassHosts(hosts []string) {
	s.originTLSVerifyBypassHosts.Store(normalizeHosts(hosts))
	s.tr.CloseIdleConnections()
}

// OriginTLSVerifyBypassHosts returns current origin TLS verification exceptions.
func (s *Server) OriginTLSVerifyBypassHosts() []string {
	return copyHosts(s.originTLSVerifyBypassHosts.Load())
}

func copyHosts(p *[]string) []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(*p))
	copy(out, *p)
	return out
}

func (s *Server) shouldBypassOriginTLSVerify(host string) bool {
	p := s.originTLSVerifyBypassHosts.Load()
	if p == nil {
		return false
	}
	for _, pat := range *p {
		if hostpattern.MatchHost(pat, host) {
			return true
		}
	}
	return false
}

// SetAutoBypassOnPinFailure toggles auto-adding a host to the bypass list when
// its MITM handshake fails (the SSL-pinning signal).
func (s *Server) SetAutoBypassOnPinFailure(v bool) { s.autoBypass.Store(v) }

// AutoBypassOnPinFailure reports whether auto-bypass is enabled.
func (s *Server) AutoBypassOnPinFailure() bool { return s.autoBypass.Load() }

func normalizeHosts(hosts []string) *[]string {
	seen := map[string]bool{}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return &out
}

// hasBypassHosts reports whether any passthrough pattern is configured.
func (s *Server) hasBypassHosts() bool {
	p := s.bypassHosts.Load()
	return p != nil && len(*p) > 0
}

// errSNIPeeked aborts the throwaway handshake peekClientHelloSNI runs.
var errSNIPeeked = errors.New("proxy: client hello peeked")

// sniffConn feeds a throwaway tls.Server: every byte read from the client is
// kept for replay and nothing is ever written back, so the client cannot tell
// its ClientHello was looked at.
type sniffConn struct {
	net.Conn
	seen bytes.Buffer
}

func (c *sniffConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.seen.Write(p[:n])
	return n, err
}

func (c *sniffConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *sniffConn) Close() error                { return nil }

// peekClientHelloSNI reads the client's TLS ClientHello from conn and returns
// its server name ("" when it carries none) together with a conn that replays
// the consumed bytes first, so a real handshake (ours, or the origin's on a
// passthrough) still sees the whole stream. crypto/tls does the parsing, so
// fragmented and oversized hellos are handled exactly as the MITM handshake
// would. err is the read/parse failure when no ClientHello arrived; the replay
// conn is valid either way.
func peekClientHelloSNI(conn net.Conn) (sni string, replay net.Conn, err error) {
	sc := &sniffConn{Conn: conn}
	parsed := false
	err = tls.Server(sc, &tls.Config{GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
		sni, parsed = chi.ServerName, true
		return nil, errSNIPeeked
	}}).Handshake()
	if parsed {
		err = nil
	}
	replay = conn
	if sc.seen.Len() > 0 {
		replay = &prefixedConn{Conn: conn, prefix: sc.seen.Bytes()}
	}
	return sni, replay, err
}

// shouldBypassTLS reports whether CONNECTs to host should skip MITM.
func (s *Server) shouldBypassTLS(host string) bool {
	p := s.bypassHosts.Load()
	if p == nil {
		return false
	}
	for _, pat := range *p {
		if hostpattern.MatchHost(pat, host) {
			return true
		}
	}
	return false
}

// addBypassHost appends host to the bypass list (if absent) and fires
// OnBypassAdded with the full updated list so the control plane can persist it.
// A no-op if host is already covered.
func (s *Server) addBypassHost(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	s.bypassMu.Lock()
	cur := s.TLSBypassHosts()
	for _, pat := range cur {
		if hostpattern.MatchHost(pat, host) {
			s.bypassMu.Unlock()
			return
		}
	}
	next := append(cur, host)
	s.bypassHosts.Store(&next)
	s.bypassVersion.Add(1)
	cb := s.OnBypassAdded
	s.bypassMu.Unlock()
	if cb != nil {
		s.notifyBypassAdded(cb)
	}
}

func (s *Server) notifyBypassAdded(cb func([]string)) {
	for {
		version := s.bypassVersion.Load()
		cb(s.TLSBypassHosts())
		if s.bypassVersion.Load() == version {
			return
		}
	}
}

// tunnelRaw splices the already-CONNECT-accepted client connection to the origin
// without terminating TLS, so the client's handshake (and pinning) reaches the
// real server. The first passthrough per host is recorded as an informational
// flow so the operator can see the domain is intentionally not intercepted.
//
// dialHost is the CONNECT target and is what gets dialled; host is the name the
// passthrough is recorded under, which differs for "CONNECT <IP>" matched by
// its ClientHello SNI (the client chose that IP, so it is dialled as given).
func (s *Server) tunnelRaw(client net.Conn, dialHost, host string, port int, r *http.Request, suppression suppressionSnapshot) {
	up, err := s.dialRawUpstream(dialHost, port)
	if err != nil {
		writeSimpleResponse(client, http.StatusBadGateway, "tls-bypass dial: "+err.Error())
		return
	}
	defer up.Close()

	s.recordBypassOnce(host, port, client.RemoteAddr().String(), r, suppression)

	spliceHalfClose(client, up, s.halfCloseIdleBound())
}

// rawHalfCloseIdleTimeout bounds how long a passthrough tunnel may stay silent
// once one side has finished sending. It only applies after a half-close; an
// open tunnel in both directions has no application-level timeout.
const rawHalfCloseIdleTimeout = tunnelIdleTimeout

func (s *Server) halfCloseIdleBound() time.Duration {
	if s.halfCloseIdle > 0 {
		return s.halfCloseIdle
	}
	return rawHalfCloseIdleTimeout
}

// spliceHalfClose relays bytes between a and b until both directions are done.
// When one direction reaches a clean EOF its peer's write side is shut down
// (TCP FIN), and the other direction keeps flowing: a client that sends its
// request and half-closes still receives the response. From then on the
// remaining direction must make progress within idle, so a peer that never
// answers and never closes cannot pin the tunnel. An error on either
// direction, or a conn that cannot half-close, tears the tunnel down at once.
// The caller closes both conns.
func spliceHalfClose(a, b net.Conn, idle time.Duration) {
	type result struct {
		dst, src net.Conn
		err      error
	}
	var lingering atomic.Bool
	done := make(chan result, 2)
	relay := func(dst, src net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if lingering.Load() {
					_ = dst.SetWriteDeadline(time.Now().Add(idle))
				}
				if _, werr := dst.Write(buf[:n]); werr != nil {
					done <- result{dst, src, werr}
					return
				}
				if lingering.Load() {
					_ = src.SetReadDeadline(time.Now().Add(idle))
				}
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				done <- result{dst, src, err}
				return
			}
		}
	}
	go relay(b, a)
	go relay(a, b)
	first := <-done
	if first.err != nil || !closeWrite(first.dst) {
		return // defers in the caller close both; the other relay unblocks
	}
	// first.dst is the conn the remaining direction reads from.
	lingering.Store(true)
	_ = first.dst.SetReadDeadline(time.Now().Add(idle))
	_ = first.src.SetWriteDeadline(time.Now().Add(idle))
	<-done
}

// closeWrite shuts down the write side of c, reporting whether c supports it.
func closeWrite(c net.Conn) bool {
	cw, ok := c.(interface{ CloseWrite() error })
	return ok && cw.CloseWrite() == nil
}

// dialRawUpstream opens a plain TCP connection to the origin (through the chained
// upstream proxy's CONNECT if one is configured), for raw TLS passthrough.
func (s *Server) dialRawUpstream(host string, port int) (net.Conn, error) {
	if s.isOwnListenerTarget(host, port) {
		return nil, errSelfTarget
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := s.selfDialer(30 * time.Second)
	if up := s.upstream.Load(); up != nil {
		if isHTTPUpstream(up) {
			return dialViaUpstream(d, up, addr, s.upstreamProxyTLSConfig(up.Hostname()))
		}
		if isSOCKSUpstream(up) {
			return dialViaSOCKS(context.Background(), up, "tcp", addr)
		}
		return nil, fmt.Errorf("unsupported upstream proxy scheme %q", up.Scheme)
	}
	return d.Dial("tcp", addr)
}

// recordBypassOnce persists at most one informational flow for a bypassed host.
func (s *Server) recordBypassOnce(host string, port int, clientAddr string, r *http.Request, suppression suppressionSnapshot) {
	if !s.claimBypassRecord(host, suppression) {
		return
	}
	if !s.recordBypass(host, port, clientAddr, r, suppression) {
		s.bypassSeen.Delete(host)
	}
}

func (s *Server) claimBypassRecord(host string, suppression suppressionSnapshot) bool {
	if suppression.suppresses(host) {
		return false
	}
	_, seen := s.bypassSeen.LoadOrStore(host, struct{}{})
	return !seen
}

func (s *Server) recordBypass(host string, port int, clientAddr string, r *http.Request, suppression suppressionSnapshot) bool {
	flow := suppression.newFlow(&store.Flow{
		TS:          time.Now(),
		Method:      "CONNECT",
		Scheme:      "https",
		Host:        host,
		Port:        port,
		Path:        "(tls passthrough — not intercepted)",
		HTTPVersion: "HTTP/1.1",
		ClientAddr:  clientAddr,
		Flags:       store.FlagTLSBypassed,
		ReqHeaders:  headerWithHost(r),
	})
	s.record(flow)
	if flow.ID == 0 {
		return false
	}
	s.st.AddFlowTagsNonBlocking(flow.ID, []string{"tls-bypassed"})
	return true
}
