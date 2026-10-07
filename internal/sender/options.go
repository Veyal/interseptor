package sender

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// RawHeader is one header line exactly as it should appear on the wire.
type RawHeader struct {
	Name  string
	Value string
}

// Meta ties a send to the collection run that produced it. The sender does not
// interpret it; it is handed back to SendOptions.OnFlow after each flow is
// persisted so callers can write flow context rows.
type Meta struct {
	RunID     string
	ItemID    string
	Iteration int
	Phase     string // e.g. "pre", "main", "script"
}

// SendOptions tunes one send. A nil *SendOptions (the Repeater/Intruder path)
// keeps the historical behaviour: no redirects, no timeout, no TLS verification,
// the globally configured upstream proxy and no destination guard.
type SendOptions struct {
	Timeout         time.Duration // whole exchange incl. body; 0 = none
	FollowRedirects bool          // each hop is its own flow
	MaxRedirects    int           // 0 = 10 when FollowRedirects
	VerifyTLS       bool          // default false: origin certs are not verified
	ServerName      string        // SNI / verification name override
	ClientCertPEM   []byte        // optional mTLS client certificate chain
	ClientKeyPEM    []byte
	ProxyURL        string            // overrides the global upstream proxy
	NoProxy         bool              // ignore the global upstream proxy
	DNSOverride     map[string]string // lowercase host -> IP literal
	Guard           *IPGuard          // dial-time destination vetting
	// RawHeaders, when non-empty, are written verbatim in order (duplicates
	// kept, no canonicalisation) over HTTP/1.1. Session headers and the token
	// macro are not injected: the caller owns the exact wire request. Not
	// supported through HTTP(S) upstream proxies.
	RawHeaders []RawHeader
	Meta       Meta
	// OnFlow runs after each flow (every redirect hop) has been persisted and
	// the sender's persist hook has returned.
	OnFlow func(*store.Flow, Meta)
}

// Send issues r and follows redirects when r.Options asks for it.
func (s *Sender) Send(r Request) (*store.Flow, error) {
	flow, err := s.sendOne(r)
	o := r.Options
	if err != nil || o == nil || !o.FollowRedirects {
		return flow, err
	}
	max := o.MaxRedirects
	if max <= 0 {
		max = 10
	}
	for hop := 0; hop < max; hop++ {
		next, ok := nextRedirect(r, flow)
		if !ok {
			break
		}
		r = next
		if flow, err = s.sendOne(r); err != nil {
			return flow, err
		}
	}
	return flow, nil
}

// nextRedirect builds the follow-up request for a 3xx flow.
func nextRedirect(r Request, flow *store.Flow) (Request, bool) {
	switch flow.Status {
	case 301, 302, 303, 307, 308:
	default:
		return r, false
	}
	loc := http.Header(flow.ResHeaders).Get("Location")
	base, err := url.Parse(r.URL)
	if loc == "" || err != nil {
		return r, false
	}
	target, err := base.Parse(loc)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return r, false
	}
	n := r
	n.URL = target.String()
	n.Host = ""
	n.retried401 = false
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	dropBody := flow.Status == 303 && method != http.MethodHead ||
		(flow.Status == 301 || flow.Status == 302) && method == http.MethodPost
	if dropBody {
		n.Method, n.Body = http.MethodGet, nil
	}
	crossHost := !strings.EqualFold(base.Host, target.Host)
	skip := func(k string) bool {
		c := http.CanonicalHeaderKey(k)
		if c == "Host" || dropBody && (c == "Content-Length" || c == "Content-Type" || c == "Transfer-Encoding") {
			return true
		}
		return crossHost && (c == "Authorization" || c == "Cookie" || c == "Proxy-Authorization")
	}
	n.Headers = make(map[string][]string, len(r.Headers))
	for k, vs := range r.Headers {
		if !skip(k) {
			n.Headers[k] = append([]string(nil), vs...)
		}
	}
	if r.Options != nil {
		oc := *r.Options
		oc.RawHeaders = nil
		for _, h := range r.Options.RawHeaders {
			if !skip(h.Name) {
				oc.RawHeaders = append(oc.RawHeaders, h)
			}
		}
		n.Options = &oc
	}
	return n, true
}

// persistFor persists the flow and then runs the caller's OnFlow hook.
func (s *Sender) persistFor(r Request, flow *store.Flow) {
	s.persist(flow)
	if o := r.Options; o != nil && o.OnFlow != nil {
		o.OnFlow(flow, o.Meta)
	}
}

// do performs the round trip, honouring per-send options.
func (s *Sender) do(r Request, req *http.Request) (*http.Response, error) {
	o := r.Options
	if o == nil {
		return s.cl.Do(req)
	}
	if len(o.RawHeaders) > 0 {
		return s.rawRoundTrip(req, r.Body, o)
	}
	cl, err := s.clientFor(o)
	if err != nil {
		return nil, err
	}
	return cl.Do(req)
}

// --- client cache ---------------------------------------------------------

const maxOptionClients = 32

type optionClients struct {
	mu sync.Mutex
	m  map[string]*http.Client
}

func (o *SendOptions) cacheKey() string {
	dns := make([]string, 0, len(o.DNSOverride))
	for k, v := range o.DNSOverride {
		dns = append(dns, strings.ToLower(k)+"="+v)
	}
	sort.Strings(dns)
	cert := sha256.Sum256(append(append([]byte{}, o.ClientCertPEM...), o.ClientKeyPEM...))
	return fmt.Sprintf("v%t|sn=%s|c=%x|p=%s|np=%t|dns=%v|g=%s",
		o.VerifyTLS, o.ServerName, cert[:8], o.ProxyURL, o.NoProxy, dns, o.Guard.fingerprint())
}

// clientFor returns the cached client for the transport-affecting parts of o.
func (s *Sender) clientFor(o *SendOptions) (*http.Client, error) {
	key := o.cacheKey()
	s.optCl.mu.Lock()
	defer s.optCl.mu.Unlock()
	if c, ok := s.optCl.m[key]; ok {
		return c, nil
	}
	tr, err := s.newOptionsTransport(o)
	if err != nil {
		return nil, err
	}
	if s.optCl.m == nil || len(s.optCl.m) >= maxOptionClients {
		s.closeOptionClientsLocked()
		s.optCl.m = map[string]*http.Client{}
	}
	c := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.optCl.m[key] = c
	return c, nil
}

func (s *Sender) closeOptionClientsLocked() {
	for _, c := range s.optCl.m {
		if tr, ok := c.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
}

// closeOptionClients drops idle connections of every cached client (called when
// the global upstream proxy or its CA changes).
func (s *Sender) closeOptionClients() {
	s.optCl.mu.Lock()
	s.closeOptionClientsLocked()
	s.optCl.mu.Unlock()
}

func (s *Sender) effectiveUpstream(o *SendOptions) *url.URL {
	switch {
	case o.NoProxy:
		return nil
	case o.ProxyURL != "":
		u, err := parseUpstream(o.ProxyURL)
		if err != nil {
			return nil
		}
		return u
	}
	return s.upstream.Load()
}

func parseUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if !senderSupportedUpstreamScheme(u.Scheme) {
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	return u, nil
}

func (s *Sender) tlsConfigFor(o *SendOptions, host string) (*tls.Config, error) {
	cfg := &tls.Config{InsecureSkipVerify: !o.VerifyTLS, ServerName: host} //nolint:gosec // opt-in verification; default mirrors Repeater
	if o.ServerName != "" {
		cfg.ServerName = o.ServerName
	}
	if len(o.ClientCertPEM) > 0 || len(o.ClientKeyPEM) > 0 {
		cert, err := tls.X509KeyPair(o.ClientCertPEM, o.ClientKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (s *Sender) newOptionsTransport(o *SendOptions) (*http.Transport, error) {
	if o.ProxyURL != "" {
		if _, err := parseUpstream(o.ProxyURL); err != nil {
			return nil, err
		}
	}
	if _, err := s.tlsConfigFor(o, ""); err != nil {
		return nil, err
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		up := s.effectiveUpstream(o)
		if up != nil && !senderSOCKSUpstream(up) && addr == senderUpstreamAddress(up) {
			return senderNetDialer().DialContext(ctx, network, addr) // proxy hop, vetted in Proxy()
		}
		return guardedDial(ctx, o.Guard, o.DNSOverride, network, addr, func(ctx context.Context, n, a string) (net.Conn, error) {
			return s.dialVia(up, ctx, n, a)
		})
	}
	return &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			up := s.effectiveUpstream(o)
			if up == nil || senderSOCKSUpstream(up) {
				return nil, nil
			}
			if o.Guard != nil {
				port, _ := strconv.Atoi(req.URL.Port())
				if port == 0 {
					port = defaultPort(req.URL.Scheme)
				}
				if err := o.Guard.CheckHost(req.Context(), req.URL.Hostname(), port); err != nil {
					return nil, err
				}
			}
			return up, nil
		},
		DialContext: dial,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			raw, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			cfg, err := s.tlsConfigFor(o, host)
			if err != nil {
				raw.Close()
				return nil, err
			}
			if up := s.effectiveUpstream(o); up != nil && up.Scheme == "https" && addr == senderUpstreamAddress(up) {
				cfg = &tls.Config{ServerName: up.Hostname(), RootCAs: s.upstreamProxyRoots.Load()}
			}
			c := tls.Client(raw, cfg)
			if err := c.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return c, nil
		},
		DisableCompression:  true,
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}, nil
}

// --- raw header wire path ---------------------------------------------------

type closeWith struct {
	io.ReadCloser
	extra func()
}

func (c closeWith) Close() error { err := c.ReadCloser.Close(); c.extra(); return err }

func (s *Sender) rawRoundTrip(req *http.Request, body []byte, o *SendOptions) (*http.Response, error) {
	up := s.effectiveUpstream(o)
	if up != nil && !senderSOCKSUpstream(up) {
		return nil, fmt.Errorf("raw headers are not supported through an HTTP(S) upstream proxy")
	}
	ctx := req.Context()
	u := req.URL
	port := u.Port()
	if port == "" {
		port = strconv.Itoa(defaultPort(u.Scheme))
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	conn, err := guardedDial(ctx, o.Guard, o.DNSOverride, "tcp", addr, func(ctx context.Context, n, a string) (net.Conn, error) {
		return s.dialVia(up, ctx, n, a)
	})
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		cfg, err := s.tlsConfigFor(o, u.Hostname())
		if err != nil {
			conn.Close()
			return nil, err
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	if _, err := conn.Write(buildRawRequest(req.Method, u, o.RawHeaders, body)); err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		stop()
		conn.Close()
		return nil, err
	}
	resp.Body = closeWith{resp.Body, func() { stop(); conn.Close() }}
	return resp, nil
}

// buildRawRequest serialises the request with headers in the given order.
// Host and Content-Length are added only when the caller did not supply them.
func buildRawRequest(method string, u *url.URL, hdrs []RawHeader, body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", method, u.RequestURI())
	has := map[string]bool{}
	for _, h := range hdrs {
		has[strings.ToLower(h.Name)] = true
	}
	if !has["host"] {
		fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	}
	for _, h := range hdrs {
		fmt.Fprintf(&b, "%s: %s\r\n", h.Name, h.Value)
	}
	if len(body) > 0 && !has["content-length"] && !has["transfer-encoding"] {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	}
	if !has["connection"] {
		b.WriteString("Connection: close\r\n")
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}
