package proxy

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/hostpattern"
	"github.com/Veyal/interseptor/internal/store"
)

// SetTLSBypassHosts replaces the set of host patterns that are tunneled raw
// (no MITM). Patterns are exact or "*.wildcard" (see hostpattern). Entries are
// trimmed, lower-cased, de-duplicated; blanks are dropped.
func (s *Server) SetTLSBypassHosts(hosts []string) {
	s.bypassHosts.Store(normalizeHosts(hosts))
}

// TLSBypassHosts returns the current bypass patterns (a copy).
func (s *Server) TLSBypassHosts() []string {
	p := s.bypassHosts.Load()
	if p == nil {
		return nil
	}
	out := make([]string, len(*p))
	copy(out, *p)
	return out
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
	if host == "" || s.shouldBypassTLS(host) {
		return
	}
	cur := s.TLSBypassHosts()
	next := append(cur, host)
	s.bypassHosts.Store(&next)
	if s.OnBypassAdded != nil {
		s.OnBypassAdded(next) // fired outside any lock; callback must be thread-safe
	}
}

// tunnelRaw splices the already-CONNECT-accepted client connection to the origin
// without terminating TLS, so the client's handshake (and pinning) reaches the
// real server. The first passthrough per host is recorded as an informational
// flow so the operator can see the domain is intentionally not intercepted.
func (s *Server) tunnelRaw(client net.Conn, host string, port int, r *http.Request) {
	up, err := s.dialRawUpstream(host, port)
	if err != nil {
		writeSimpleResponse(client, http.StatusBadGateway, "tls-bypass dial: "+err.Error())
		return
	}
	defer up.Close()

	if _, seen := s.bypassSeen.LoadOrStore(host, struct{}{}); !seen {
		s.recordBypass(host, port, client.RemoteAddr().String(), r)
	}

	done := make(chan struct{}, 2)
	go func() { io.Copy(up, client); done <- struct{}{} }()
	go func() { io.Copy(client, up); done <- struct{}{} }()
	<-done // one side closed; unblock and tear the tunnel down (defers close both)
}

// dialRawUpstream opens a plain TCP connection to the origin (through the chained
// upstream proxy's CONNECT if one is configured), for raw TLS passthrough.
func (s *Server) dialRawUpstream(host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if up := s.upstream.Load(); up != nil && (up.Scheme == "http" || up.Scheme == "https" || up.Scheme == "") {
		return dialViaUpstream(d, up, addr)
	}
	return d.Dial("tcp", addr)
}

// recordBypass persists a single informational flow marking a host as passed
// through untouched, so it is visible in history/activity (deduped per host).
func (s *Server) recordBypass(host string, port int, clientAddr string, r *http.Request) {
	flow := &store.Flow{
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
	}
	s.record(flow)
	if flow.ID != 0 {
		_, _ = s.st.AddFlowTags(flow.ID, []string{"tls-bypassed"})
	}
}
