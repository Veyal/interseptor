package sender

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

func newOptSender(t *testing.T) *Sender {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, capture.New(st))
}

func TestOptionsDefaultsNoRedirectNoGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", 302)
			return
		}
		w.Write([]byte("b"))
	}))
	defer srv.Close()
	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: srv.URL + "/a"})
	if err != nil || f.Status != 302 {
		t.Fatalf("default must not follow redirects: %v %+v", err, f)
	}
}

func TestOptionsFollowRedirectsEachHopIsAFlowWithMeta(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", 302)
			return
		}
		w.Write([]byte("b"))
	}))
	defer srv.Close()
	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: srv.URL + "/a", Options: &SendOptions{
		FollowRedirects: true, Meta: Meta{RunID: "r1", Phase: "main"},
		OnFlow: func(f *store.Flow, m Meta) {
			mu.Lock()
			seen = append(seen, m.RunID+":"+f.Path)
			mu.Unlock()
		}}})
	if err != nil || f.Status != 200 || f.Path != "/b" {
		t.Fatalf("final = %v %+v", err, f)
	}
	if strings.Join(seen, ",") != "r1:/a,r1:/b" {
		t.Fatalf("OnFlow calls = %v", seen)
	}
}

func TestOptionsRedirectGuardedPerHop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/x", 302)
	}))
	defer srv.Close()
	snd := newOptSender(t)
	// hop one is allowed by name (localhost); the 127.0.0.1 literal hop is not.
	g := &IPGuard{BlockPrivate: true, AllowHosts: []string{"localhost"}}
	u := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	f, err := snd.Send(Request{URL: u, Options: &SendOptions{FollowRedirects: true, Guard: g}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != 502 || !strings.Contains(f.Error, "IP guard") {
		t.Fatalf("redirect hop to 127.0.0.1 must be blocked: status=%d err=%q", f.Status, f.Error)
	}
}

func TestOptionsGuardBlocksLoopbackAndRecordsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hit")) }))
	defer srv.Close()
	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: srv.URL, Options: &SendOptions{Guard: &IPGuard{BlockPrivate: true}}})
	if err != nil || f.Status != 502 || !strings.Contains(f.Error, ErrGuardBlocked.Error()) {
		t.Fatalf("want guard block flow, got %v %+v", err, f)
	}
	// Exact-host allow lets the same destination through.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	f, err = snd.Send(Request{URL: "http://localhost:" + port, Options: &SendOptions{Guard: &IPGuard{BlockPrivate: true, AllowHosts: []string{"localhost"}}}})
	if err != nil || f.Status != 200 {
		t.Fatalf("allowed host: %v %+v", err, f)
	}
	// Own-listener refusal beats the allow list.
	pn := 0
	for _, c := range port {
		pn = pn*10 + int(c-'0')
	}
	f, _ = snd.Send(Request{URL: "http://localhost:" + port, Options: &SendOptions{Guard: &IPGuard{BlockPrivate: true, AllowHosts: []string{"localhost"}, OwnPorts: []int{pn}}}})
	if f.Status != 502 || !strings.Contains(f.Error, "own listener") {
		t.Fatalf("own port must be refused: %+v", f)
	}
}

func TestOptionsDNSOverrideRebindBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hit")) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	snd := newOptSender(t)
	o := &SendOptions{DNSOverride: map[string]string{"app.example.com": "127.0.0.1"}, Guard: &IPGuard{BlockPrivate: true}}
	f, _ := snd.Send(Request{URL: "http://app.example.com:" + port, Options: o})
	if f.Status != 502 || !strings.Contains(f.Error, "non-public") {
		t.Fatalf("name resolving to loopback must be blocked: %+v", f)
	}
	o2 := &SendOptions{DNSOverride: map[string]string{"app.example.com": "127.0.0.1"}}
	f, _ = snd.Send(Request{URL: "http://app.example.com:" + port, Options: o2})
	if f.Status != 200 {
		t.Fatalf("unguarded dns override should connect: %+v", f)
	}
}

func TestOptionsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(500 * time.Millisecond) }))
	defer srv.Close()
	snd := newOptSender(t)
	f, _ := snd.Send(Request{URL: srv.URL, Options: &SendOptions{Timeout: 50 * time.Millisecond}})
	if f.Status != 502 || f.Error == "" {
		t.Fatalf("timeout should record errored flow: %+v", f)
	}
}

func TestOptionsClientCacheReuse(t *testing.T) {
	snd := newOptSender(t)
	a, _ := snd.clientFor(&SendOptions{VerifyTLS: true})
	b, _ := snd.clientFor(&SendOptions{VerifyTLS: true, Timeout: time.Second, FollowRedirects: true})
	c, _ := snd.clientFor(&SendOptions{VerifyTLS: false})
	if a != b {
		t.Fatal("non-transport options must share a client")
	}
	if a == c {
		t.Fatal("verify flag must split clients")
	}
	if _, err := snd.clientFor(&SendOptions{ProxyURL: "ftp://x"}); err == nil {
		t.Fatal("bad proxy scheme must error")
	}
}

func TestOptionsRawHeadersOrderAndDuplicates(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var lines []string
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				return
			}
			l = strings.TrimRight(l, "\r\n")
			if l == "" {
				break
			}
			lines = append(lines, l)
		}
		got <- lines
		c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
	}()
	snd := newOptSender(t)
	f, err := snd.Send(Request{Method: "GET", URL: "http://" + ln.Addr().String() + "/p?q=1", Options: &SendOptions{
		RawHeaders: []RawHeader{{"zeta", "1"}, {"X-Dup", "a"}, {"Alpha", "2"}, {"x-dup", "b"}}}})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	lines := <-got
	want := []string{"GET /p?q=1 HTTP/1.1", "Host: " + ln.Addr().String(), "zeta: 1", "X-Dup: a", "Alpha: 2", "x-dup: b", "Connection: close"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("wire =\n%v\nwant\n%v", lines, want)
	}
	if len(f.ReqHeaders["X-Dup"]) != 2 {
		t.Fatalf("flow must keep duplicates: %v", f.ReqHeaders)
	}
}

func TestOptionsRawHeadersRejectedThroughHTTPProxy(t *testing.T) {
	snd := newOptSender(t)
	f, _ := snd.Send(Request{URL: "http://example.com/", Options: &SendOptions{ProxyURL: "http://127.0.0.1:9", RawHeaders: []RawHeader{{"A", "b"}}}})
	if f.Status != 502 || !strings.Contains(f.Error, "raw headers") {
		t.Fatalf("%+v", f)
	}
}

func TestNextRedirectStripsCredentialsCrossHost(t *testing.T) {
	r := Request{Method: "POST", URL: "https://a.example.com/x", Body: []byte("b"),
		Headers: map[string][]string{"Authorization": {"t"}, "Cookie": {"c"}, "X-Keep": {"1"}, "Content-Type": {"a/b"}}}
	fl := &store.Flow{Status: 302, ResHeaders: map[string][]string{"Location": {"https://b.example.com/y"}}}
	n, ok := nextRedirect(r, fl)
	if !ok || n.Method != "GET" || n.Body != nil || n.URL != "https://b.example.com/y" {
		t.Fatalf("%+v", n)
	}
	if _, has := n.Headers["Authorization"]; has || n.Headers["X-Keep"] == nil || n.Headers["Content-Type"] != nil {
		t.Fatalf("headers %v", n.Headers)
	}
}

// SendOptions.Timeout is documented as the whole exchange: following redirects
// must not restart it per hop, or a chain of slow hops runs for up to
// MaxRedirects × Timeout instead of the budget the caller set.
func TestOptionsTimeoutSpansRedirectChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		http.Redirect(w, r, "/hop", http.StatusFound)
	}))
	defer srv.Close()
	snd := newOptSender(t)
	start := time.Now()
	f, err := snd.Send(Request{URL: srv.URL + "/start", Options: &SendOptions{
		FollowRedirects: true, MaxRedirects: 5, Timeout: 200 * time.Millisecond,
	}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 450*time.Millisecond {
		t.Fatalf("redirect chain took %v with a 200ms timeout", elapsed)
	}
	if f.Status != http.StatusBadGateway || f.Error == "" {
		t.Fatalf("final flow = %+v, want the timed-out hop recorded as a 502", f)
	}
}

// startConnectProxy is a minimal HTTP CONNECT proxy: enough to exercise a
// proxied HTTPS send without an external dependency.
func startConnectProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				up, err := net.Dial("tcp", req.Host)
				if err != nil {
					return
				}
				defer up.Close()
				if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}
				go func() { _, _ = io.Copy(up, br) }()
				_, _ = io.Copy(c, up)
			}(c)
		}
	}()
	return "http://" + ln.Addr().String()
}

// Through an upstream proxy net/http performs the origin TLS handshake itself
// (after CONNECT), so the per-send TLS policy has to live on the transport too.
// Before that, a proxied send to a self-signed origin — the normal pentest
// target — failed verification even though VerifyTLS defaults to false.
func TestOptionsProxiedHTTPSKeepsPermissiveTLS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer origin.Close()
	proxy := startConnectProxy(t)
	snd := newOptSender(t)

	f, err := snd.Send(Request{URL: origin.URL, Options: &SendOptions{ProxyURL: proxy}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.Status != http.StatusOK {
		t.Fatalf("proxied HTTPS to a self-signed origin = %d %q; want 200 (VerifyTLS is off by default)", f.Status, f.Error)
	}
}

// The other direction: an explicit VerifyTLS must still reject a self-signed
// origin when the send goes through a proxy (the fix must not simply disable
// verification everywhere).
func TestOptionsProxiedHTTPSHonoursVerifyTLS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer origin.Close()
	proxy := startConnectProxy(t)
	snd := newOptSender(t)

	f, err := snd.Send(Request{URL: origin.URL, Options: &SendOptions{ProxyURL: proxy, VerifyTLS: true}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.Status != http.StatusBadGateway || !strings.Contains(f.Error, "certificate") {
		t.Fatalf("VerifyTLS through a proxy = %d %q; want a certificate failure", f.Status, f.Error)
	}
}

// The raw-headers wire path writes HTTP/1.1 bytes, so it must not offer "h2"
// ALPN even when the HTTP2 option is set: a server that picks h2 answers with
// binary frames that ReadResponse cannot parse.
func TestOptionsRawHeadersStayHTTP11WithHTTP2(t *testing.T) {
	seen := make(chan string, 1)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Proto + "|" + r.Header.Get("X-Raw")
		io.WriteString(w, "ok")
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()

	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: origin.URL + "/x", Options: &SendOptions{
		HTTP2: true, RawHeaders: []RawHeader{{"X-Raw", "1"}}}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.Status != http.StatusOK {
		t.Fatalf("raw headers with HTTP2 = %d %q; the raw wire path must stay HTTP/1.1", f.Status, f.Error)
	}
	if got := <-seen; got != "HTTP/1.1|1" {
		t.Fatalf("origin saw %q, want HTTP/1.1 with the raw header", got)
	}
	if f.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("recorded version = %q, want HTTP/1.1", f.HTTPVersion)
	}
}

// An invalid ProxyURL must be rejected on the raw wire path too. Silently
// ignoring it sends the request directly, bypassing the configured egress.
func TestOptionsRawHeadersRejectInvalidProxy(t *testing.T) {
	var direct atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	snd := newOptSender(t)

	f, err := snd.Send(Request{URL: srv.URL + "/x", Options: &SendOptions{
		ProxyURL: "ftp://127.0.0.1:9", RawHeaders: []RawHeader{{"X-A", "1"}}}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if direct.Load() != 0 {
		t.Fatal("invalid proxy was silently bypassed: the request went direct")
	}
	if f.Status != http.StatusBadGateway || !strings.Contains(f.Error, "proxy") {
		t.Fatalf("raw send with an invalid proxy = %d %q; want a proxy error", f.Status, f.Error)
	}
}

// A redirect that stays on the same host must keep the wire Host override: the
// override selects a virtual host, and dropping it on the next hop silently
// retargets the follow-up request at the default vhost.
func TestOptionsRedirectKeepsHostOverrideOnSameHost(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	snd := newOptSender(t)

	f, err := snd.Send(Request{URL: srv.URL + "/a", Host: "vhost.example",
		Options: &SendOptions{FollowRedirects: true}})
	if err != nil || f.Status != http.StatusOK {
		t.Fatalf("Send: %v %+v", err, f)
	}
	mu.Lock()
	got := append([]string(nil), hosts...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "vhost.example" || got[1] != "vhost.example" {
		t.Fatalf("wire Host per hop = %v, want the override on both same-host hops", got)
	}

	// A redirect to a different hostname must still drop it. Two loopback
	// servers share 127.0.0.1, so the redirect targets "localhost" instead.
	var otherHost string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHost = r.Host
		io.WriteString(w, "ok")
	}))
	defer other.Close()
	_, otherPort, _ := net.SplitHostPort(strings.TrimPrefix(other.URL, "http://"))
	crossTarget := "http://localhost:" + otherPort + "/c"
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, crossTarget, http.StatusFound)
	}))
	defer srv2.Close()
	if _, err := snd.Send(Request{URL: srv2.URL + "/a", Host: "vhost.example",
		Options: &SendOptions{FollowRedirects: true}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if strings.Contains(otherHost, "vhost.example") {
		t.Fatalf("cross-host redirect leaked the Host override: %q", otherHost)
	}
}

// The transport-level TLS policy must not cost the HTTP/2 path: a proxied HTTPS
// send with HTTP2 still negotiates h2, and the ServerName override is honoured
// for the origin verification (it used to be ignored once a proxy was set).
func TestOptionsProxiedHTTPSHTTP2AndServerName(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()
	proxy := startConnectProxy(t)
	snd := newOptSender(t)

	f, err := snd.Send(Request{URL: origin.URL, Options: &SendOptions{HTTP2: true, ProxyURL: proxy}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.Status != http.StatusOK || f.HTTPVersion != "HTTP/2.0" {
		t.Fatalf("proxied HTTP2 = %d %q proto=%q; want 200 over HTTP/2.0", f.Status, f.Error, f.HTTPVersion)
	}

	// The httptest certificate covers 127.0.0.1; asking to verify a name it does
	// not cover must fail, proving the override reached the origin handshake.
	f2, err := snd.Send(Request{URL: origin.URL, Options: &SendOptions{
		VerifyTLS: true, ServerName: "wrong.example", ProxyURL: proxy}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f2.Status != http.StatusBadGateway || !strings.Contains(f2.Error, "certificate") {
		t.Fatalf("ServerName override through a proxy = %d %q; want a certificate failure", f2.Status, f2.Error)
	}
}
