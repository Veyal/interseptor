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
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
	"golang.org/x/net/http2"
)

// streamingHandler sends one event, flushes, then blocks until release is
// closed (or the client goes away) before sending the second event. A proxy
// that buffers the response until completion never delivers the first event
// while the origin is blocked.
func streamingHandler(contentType string, release <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Origin", "streaming")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: two\n\n")
	}
}

// readLineWithin reads one line from r, failing the test if it does not arrive
// within d.
func readLineWithin(t *testing.T, r *bufio.Reader, d time.Duration) string {
	t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- res{line, err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read streamed line: %v", got.err)
		}
		return got.line
	case <-time.After(d):
		t.Fatalf("first streamed event not delivered within %v: response is buffered until completion", d)
	}
	return ""
}

func newStreamingProxy(t *testing.T, eng *intercept.Engine, ca *tlsca.CA) (*Server, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s, capture.New(s), ca, eng, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	return srv, ln.Addr().String()
}

func streamingEngine(t *testing.T, rules []store.Rule, holdResponses bool) *intercept.Engine {
	t.Helper()
	eng := intercept.New()
	if err := eng.SetRules(rules); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	eng.SetResponseEnabled(holdResponses)
	return eng
}

func TestPlainHTTPStreamsResponsesBeforeCompletion(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		rules       []store.Rule
		hold        bool
		wantHeader  string // expected X-Origin after header rules
	}{
		{name: "sse no rules", contentType: "text/event-stream"},
		{name: "chunked no rules", contentType: "text/plain"},
		{name: "sse non-matching header rule", contentType: "text/event-stream",
			rules: []store.Rule{{Enabled: true, Type: "res-header", Match: "nomatch-xyz", Replace: "x"}}},
		{name: "chunked matching header rule is applied while streaming", contentType: "text/plain",
			rules:      []store.Rule{{Enabled: true, Type: "res-header", Match: "X-Origin: streaming", Replace: "X-Origin: rewritten"}},
			wantHeader: "rewritten"},
		{name: "sse body rule is skipped", contentType: "text/event-stream",
			rules: []store.Rule{{Enabled: true, Type: "res-body", Match: "one", Replace: "ONE"}}},
		{name: "sse response hold is skipped", contentType: "text/event-stream", hold: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			origin := httptest.NewServer(streamingHandler(tc.contentType, release))
			defer origin.Close()
			defer close(release)

			var eng *intercept.Engine
			if tc.rules != nil || tc.hold {
				eng = streamingEngine(t, tc.rules, tc.hold)
			}
			_, proxyAddr := newStreamingProxy(t, eng, nil)
			proxyURL, _ := url.Parse("http://" + proxyAddr)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

			type result struct {
				resp *http.Response
				err  error
			}
			ch := make(chan result, 1)
			go func() {
				resp, err := client.Get(origin.URL + "/events")
				ch <- result{resp, err}
			}()
			var resp *http.Response
			select {
			case r := <-ch:
				if r.err != nil {
					t.Fatalf("GET through proxy: %v", r.err)
				}
				resp = r.resp
			case <-time.After(2 * time.Second):
				t.Fatal("response headers not delivered while origin is still streaming")
			}
			defer resp.Body.Close()
			if want := tc.wantHeader; want != "" && resp.Header.Get("X-Origin") != want {
				t.Fatalf("X-Origin = %q, want %q", resp.Header.Get("X-Origin"), want)
			}
			if got := readLineWithin(t, bufio.NewReader(resp.Body), 2*time.Second); got != "data: one\n" {
				t.Fatalf("first line = %q, want untransformed first event", got)
			}
		})
	}
}

// TestMITMStreamsResponsesBeforeCompletion covers both client legs of the
// HTTPS MITM: browsers negotiate h2, other clients HTTP/1.1.
func TestMITMStreamsResponsesBeforeCompletion(t *testing.T) {
	for _, proto := range []string{"h2", "http/1.1"} {
		for _, withRule := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s rule=%v", proto, withRule), func(t *testing.T) {
				release := make(chan struct{})
				origin := httptest.NewTLSServer(streamingHandler("text/event-stream", release))
				defer origin.Close()
				defer close(release)

				ca, err := tlsca.LoadOrCreate(t.TempDir())
				if err != nil {
					t.Fatalf("CA: %v", err)
				}
				var eng *intercept.Engine
				if withRule {
					eng = streamingEngine(t, []store.Rule{{Enabled: true, Type: "res-header", Match: "nomatch-xyz", Replace: "x"}}, false)
				}
				srv, proxyAddr := newStreamingProxy(t, eng, ca)
				srv.tr.TLSClientConfig = &tls.Config{RootCAs: originPool(origin), NextProtos: []string{"h2", "http/1.1"}}

				originHost := strings.TrimPrefix(origin.URL, "https://")
				raw, err := net.Dial("tcp", proxyAddr)
				if err != nil {
					t.Fatalf("dial proxy: %v", err)
				}
				defer raw.Close()
				fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", originHost, originHost)
				br := bufio.NewReader(raw)
				connResp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
				if err != nil || connResp.StatusCode != 200 {
					t.Fatalf("CONNECT failed: %v / %v", err, connResp)
				}
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM(ca.CertPEM())
				host, _, _ := net.SplitHostPort(originHost)
				tc := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: host, NextProtos: []string{proto}})
				if err := tc.Handshake(); err != nil {
					t.Fatalf("client handshake: %v", err)
				}

				req, _ := http.NewRequest("GET", "https://"+originHost+"/events", nil)
				var body io.Reader
				if proto == "h2" {
					cc, err := (&http2.Transport{}).NewClientConn(tc)
					if err != nil {
						t.Fatalf("h2 client conn: %v", err)
					}
					done := make(chan *http.Response, 1)
					go func() {
						resp, err := cc.RoundTrip(req)
						if err != nil {
							t.Errorf("h2 round trip: %v", err)
							close(done)
							return
						}
						done <- resp
					}()
					select {
					case resp, ok := <-done:
						if !ok {
							t.FailNow()
						}
						defer resp.Body.Close()
						body = resp.Body
					case <-time.After(2 * time.Second):
						t.Fatal("h2 response headers not delivered while origin is still streaming")
					}
				} else {
					if err := req.Write(tc); err != nil {
						t.Fatalf("write request: %v", err)
					}
					tbr := bufio.NewReader(tc)
					tc.SetReadDeadline(time.Now().Add(2 * time.Second))
					resp, err := http.ReadResponse(tbr, req)
					if err != nil {
						t.Fatalf("read response head: %v", err)
					}
					tc.SetReadDeadline(time.Time{})
					// No Body.Close: it would drain the still-open stream. Closing
					// raw (deferred) tears the tunnel down instead.
					body = resp.Body
				}
				if got := readLineWithin(t, bufio.NewReader(body), 2*time.Second); got != "data: one\n" {
					t.Fatalf("first line = %q", got)
				}
			})
		}
	}
}

// A header-only response rule must not read the body at all: the head is
// rewritten in place, Content-Length is kept, and the original head is kept
// for History only when a rule actually changed it.
func TestHeaderOnlyResponseRuleDoesNotReadBody(t *testing.T) {
	for _, tc := range []struct {
		name, match, replace string
		wantChanged          bool
	}{
		{"matching", "X-Origin: a", "X-Origin: b", true},
		{"non-matching", "nomatch-xyz", "x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := intercept.New()
			if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-header", Match: tc.match, Replace: tc.replace}}); err != nil {
				t.Fatalf("SetRules: %v", err)
			}
			srv := &Server{eng: eng}
			cr := &countReader{r: &byteNReader{left: 1 << 20}}
			resp := &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": {"text/plain"}, "X-Origin": {"a"}, "Content-Length": {"1048576"}},
				ContentLength: 1 << 20,
				Body:          io.NopCloser(cr),
				Request:       httptest.NewRequest(http.MethodGet, "http://example.com/big", nil),
			}
			flow := srv.newFlow(&store.Flow{Host: "example.com"})
			_, _, _, transformed, dropped := srv.maybeInterceptResponse(flow, resp)
			if transformed || dropped {
				t.Fatalf("header-only rule transformed=%v dropped=%v, want streamed", transformed, dropped)
			}
			if cr.n != 0 {
				t.Fatalf("header-only rule read %d body bytes, want 0", cr.n)
			}
			if got := resp.Header.Get("Content-Length"); got != "1048576" {
				t.Fatalf("Content-Length = %q, want origin value", got)
			}
			wantOrigin := "a"
			if tc.wantChanged {
				wantOrigin = "b"
			}
			if got := resp.Header.Get("X-Origin"); got != wantOrigin {
				t.Fatalf("X-Origin = %q, want %q", got, wantOrigin)
			}
			if changed := flow.OriginalResHeaders != nil; changed != tc.wantChanged {
				t.Fatalf("OriginalResHeaders recorded = %v, want %v", changed, tc.wantChanged)
			}
			if orig := http.Header(flow.OriginalResHeaders); tc.wantChanged && orig.Get("X-Origin") != "a" {
				t.Fatalf("original X-Origin = %q, want a", orig.Get("X-Origin"))
			}
		})
	}
}
