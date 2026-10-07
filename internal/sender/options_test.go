package sender

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
