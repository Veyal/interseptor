package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
	"golang.org/x/net/http2"
)

// sseHandler writes one SSE event, flushes, then blocks until release is
// closed or the client goes away — the shape of a live event stream.
func sseHandler(release <-chan struct{}) http.HandlerFunc {
	return streamHandler("text/event-stream", release)
}

// streamHandler is sseHandler with a configurable Content-Type (e.g. a
// text/plain long-poll that the streaming heuristic does not recognise).
func streamHandler(contentType string, release <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Secret", "strip-me")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
}

// readFirstEvent reads the first SSE data line from resp within d.
func readFirstEvent(t *testing.T, get func() (*http.Response, error), d time.Duration) *http.Response {
	t.Helper()
	type result struct {
		resp *http.Response
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := get()
		if err != nil {
			ch <- result{err: err}
			return
		}
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		ch <- result{resp: resp, line: line, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read first event: %v", r.err)
		}
		if r.line != "data: one\n" {
			t.Fatalf("first line = %q, want %q", r.line, "data: one\n")
		}
		t.Cleanup(func() { r.resp.Body.Close() })
		return r.resp
	case <-time.After(d):
		t.Fatalf("first SSE event not delivered within %v (response buffered)", d)
		return nil
	}
}

// drainStream lets the origin finish the stream, reads the remainder, and
// waits for the flow to be recorded so teardown doesn't race the capture.
func drainStream(t *testing.T, resp *http.Response, release func(), s *store.Store) {
	t.Helper()
	release()
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	waitFlows(t, s, 1)
}

func streamingRuleSets() map[string]func(*intercept.Engine) {
	return map[string]func(*intercept.Engine){
		"no-rules": func(*intercept.Engine) {},
		"header-only-rule": func(e *intercept.Engine) {
			if err := e.SetRules([]store.Rule{{Enabled: true, Type: "res-header", Match: "X-Secret: [^\n]*", Replace: ""}}); err != nil {
				panic(err)
			}
		},
		"body-rule": func(e *intercept.Engine) {
			if err := e.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "nomatch", Replace: "x"}}); err != nil {
				panic(err)
			}
		},
		"response-hold": func(e *intercept.Engine) { e.SetResponseEnabled(true) },
	}
}

func TestPlainHTTPStreamsSSE(t *testing.T) {
	for name, setup := range streamingRuleSets() {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			upstream := httptest.NewServer(sseHandler(release))
			defer upstream.Close()
			var once sync.Once
			rel := func() { once.Do(func() { close(release) }) }
			defer rel()

			s, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			eng := intercept.New()
			setup(eng)
			srv := New(s, capture.New(s), nil, eng, nil)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer ln.Close()
			go srv.Serve(ln)

			proxyURL, _ := url.Parse("http://" + ln.Addr().String())
			tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr}
			resp := readFirstEvent(t, func() (*http.Response, error) { return client.Get(upstream.URL + "/events") }, time.Second)
			if name == "header-only-rule" && resp.Header.Get("X-Secret") != "" {
				t.Fatalf("header rule not applied on streamed response: %v", resp.Header)
			}
			drainStream(t, resp, rel, s)
		})
	}
}

// A header-only response rule must not force buffering of every response: a
// text/plain long-poll (not matched by the streaming heuristic) still streams.
func TestHeaderOnlyRuleDoesNotBufferLongPoll(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(streamHandler("text/plain", release))
	defer upstream.Close()
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	defer rel()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	eng := intercept.New()
	streamingRuleSets()["header-only-rule"](eng)
	srv := New(s, capture.New(s), nil, eng, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	resp := readFirstEvent(t, func() (*http.Response, error) { return client.Get(upstream.URL + "/poll") }, time.Second)
	if resp.Header.Get("X-Secret") != "" {
		t.Fatalf("header rule not applied: %v", resp.Header)
	}
	drainStream(t, resp, rel, s)
	f := waitFlows(t, s, 1)[0]
	if f.OriginalResHeaders == nil || f.OriginalResHeaders["X-Secret"] == nil {
		t.Fatalf("original headers not recorded for header-rule change: %v", f.OriginalResHeaders)
	}
	if f.ResHeaders["X-Secret"] != nil {
		t.Fatalf("recorded res headers still carry stripped header: %v", f.ResHeaders)
	}
}

func TestMITMHTTP1StreamsSSEWithHeaderRule(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(sseHandler(release))
	defer upstream.Close()
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	defer rel()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	eng := intercept.New()
	streamingRuleSets()["header-only-rule"](eng)
	srv := New(s, capture.New(s), ca, eng, nil)
	srv.tr.TLSClientConfig = &tls.Config{RootCAs: originPool(upstream)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	resp := readFirstEvent(t, func() (*http.Response, error) { return client.Get(upstream.URL + "/events") }, time.Second)
	if resp.Header.Get("X-Secret") != "" {
		t.Fatalf("header rule not applied: %v", resp.Header)
	}
	drainStream(t, resp, rel, s)
}

func TestMITMHTTP2StreamsSSE(t *testing.T) {
	for name, setup := range streamingRuleSets() {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			origin := httptest.NewUnstartedServer(sseHandler(release))
			origin.EnableHTTP2 = true
			origin.StartTLS()
			defer origin.Close()
			var once sync.Once
			rel := func() { once.Do(func() { close(release) }) }
			defer rel()

			s, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			ca, err := tlsca.LoadOrCreate(t.TempDir())
			if err != nil {
				t.Fatalf("CA: %v", err)
			}
			eng := intercept.New()
			setup(eng)
			srv := New(s, capture.New(s), ca, eng, nil)
			srv.tr.TLSClientConfig = &tls.Config{RootCAs: originPool(origin), NextProtos: []string{"h2", "http/1.1"}}
			srv.tr.ForceAttemptHTTP2 = true
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer ln.Close()
			go srv.Serve(ln)

			originHost := strings.TrimPrefix(origin.URL, "https://")
			raw, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatalf("dial proxy: %v", err)
			}
			defer raw.Close()
			fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", originHost, originHost)
			br := bufio.NewReader(raw)
			if cr, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"}); err != nil || cr.StatusCode != 200 {
				t.Fatalf("CONNECT: %v / %v", err, cr)
			}
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(ca.CertPEM())
			host, _, _ := net.SplitHostPort(originHost)
			tc := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: host, NextProtos: []string{"h2"}})
			if err := tc.Handshake(); err != nil {
				t.Fatalf("handshake: %v", err)
			}
			cc, err := (&http2.Transport{}).NewClientConn(tc)
			if err != nil {
				t.Fatalf("h2 client: %v", err)
			}
			defer cc.Close()
			req, _ := http.NewRequest("GET", "https://"+originHost+"/events", nil)
			resp := readFirstEvent(t, func() (*http.Response, error) { return cc.RoundTrip(req) }, time.Second)
			if name == "header-only-rule" && resp.Header.Get("X-Secret") != "" {
				t.Fatalf("header rule not applied over h2: %v", resp.Header)
			}
			drainStream(t, resp, rel, s)
		})
	}
}
