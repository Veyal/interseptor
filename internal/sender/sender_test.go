package sender

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

func TestSenderHasNoRequestTimeout(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	if snd.cl.Timeout != 0 {
		t.Fatalf("Client.Timeout=%v; want 0 (no limit)", snd.cl.Timeout)
	}
	tr, ok := snd.cl.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type %T; want *http.Transport", snd.cl.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("ResponseHeaderTimeout=%v; want 0 (no limit)", tr.ResponseHeaderTimeout)
	}
}

func TestSendPreservesEncodedResponseEvidence(t *testing.T) {
	var encoded bytes.Buffer
	zw := gzip.NewWriter(&encoded)
	if _, err := zw.Write([]byte("encoded evidence")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(encoded.Bytes())
	}))
	defer upstream.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	flow, err := New(st, capture.New(st)).Send(Request{Method: http.MethodGet, URL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got := flow.ResHeaders["Content-Encoding"]; len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("Content-Encoding = %v, want gzip", got)
	}
	rc, err := st.OpenBody(flow.ResBodyHash)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, encoded.Bytes()) {
		t.Fatalf("stored response was transparently decoded: got %d bytes, want %d", len(raw), encoded.Len())
	}
}

func TestSendUsesConfiguredHTTPUpstreamProxy(t *testing.T) {
	proxyRequests := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "through upstream")
	}))
	defer upstream.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	snd := New(st, capture.New(st))
	if err := snd.SetUpstreamProxy(upstream.URL); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}

	flow, err := snd.Send(Request{Method: http.MethodGet, URL: "http://target.example/repeater?q=1", Flags: store.FlagRepeater})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != http.StatusOK {
		t.Fatalf("flow status=%d error=%q", flow.Status, flow.Error)
	}
	select {
	case req := <-proxyRequests:
		if req.URL.String() != "http://target.example/repeater?q=1" {
			t.Fatalf("upstream request URL=%q", req.URL.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Repeater request bypassed configured upstream proxy")
	}
}

func TestSenderUpstreamProxySupportsKnownSchemes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	snd := New(st, capture.New(st))
	for _, raw := range []string{"http://proxy.example:8080", "https://proxy.example", "socks5://proxy.example", "socks5h://proxy.example"} {
		if err := snd.SetUpstreamProxy(raw); err != nil {
			t.Errorf("SetUpstreamProxy(%q): %v", raw, err)
		}
	}
	if err := snd.SetUpstreamProxy("ftp://proxy.example"); err == nil {
		t.Fatal("unsupported upstream scheme accepted")
	}
}

func TestSendUsesConfiguredSOCKS5HUpstreamProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "through socks")
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	_, port, _ := net.SplitHostPort(targetURL.Host)
	targetURL.Host = net.JoinHostPort("localhost", port)

	socksAddr, destinations := newSenderSOCKS5Relay(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	snd := New(st, capture.New(st))
	if err := snd.SetUpstreamProxy("socks5h://" + socksAddr); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}

	flow, err := snd.Send(Request{URL: targetURL.String(), Flags: store.FlagRepeater})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != http.StatusOK {
		t.Fatalf("flow status=%d error=%q", flow.Status, flow.Error)
	}
	select {
	case got := <-destinations:
		if got != targetURL.Host {
			t.Fatalf("SOCKS5H destination=%q want=%q", got, targetURL.Host)
		}
	case <-time.After(time.Second):
		t.Fatal("Repeater request bypassed configured SOCKS5H proxy")
	}
}

func TestSendUsesConfiguredHTTPSUpstreamProxyCA(t *testing.T) {
	seen := make(chan string, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.String()
		_, _ = io.WriteString(w, "through secure upstream")
	}))
	defer upstream.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	snd := New(st, capture.New(st))
	if err := snd.SetUpstreamProxyCA(caPEM); err != nil {
		t.Fatalf("SetUpstreamProxyCA: %v", err)
	}
	if err := snd.SetUpstreamProxy(upstream.URL); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}

	flow, err := snd.Send(Request{URL: "http://target.example/secure-proxy", Flags: store.FlagRepeater})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != http.StatusOK {
		t.Fatalf("flow status=%d error=%q", flow.Status, flow.Error)
	}
	select {
	case got := <-seen:
		if got != "http://target.example/secure-proxy" {
			t.Fatalf("HTTPS upstream saw URL=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Repeater request bypassed configured HTTPS proxy")
	}
}

func newSenderSOCKS5Relay(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen SOCKS fixture: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	destinations := make(chan string, 2)
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go relaySenderSOCKS5(client, destinations)
		}
	}()
	return ln.Addr().String(), destinations
}

func relaySenderSOCKS5(client net.Conn, destinations chan<- string) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(client, greeting); err != nil || !bytes.Equal(greeting, []byte{5, 1, 0}) {
		return
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(client, header); err != nil || header[0] != 5 || header[1] != 1 || header[3] != 3 {
		return
	}
	var size [1]byte
	if _, err := io.ReadFull(client, size[:]); err != nil {
		return
	}
	host := make([]byte, int(size[0]))
	var portBytes [2]byte
	if _, err := io.ReadFull(client, host); err != nil {
		return
	}
	if _, err := io.ReadFull(client, portBytes[:]); err != nil {
		return
	}
	destination := net.JoinHostPort(string(host), strconv.Itoa(int(binary.BigEndian.Uint16(portBytes[:]))))
	destinations <- destination
	origin, err := net.Dial("tcp", destination)
	if err != nil {
		_, _ = client.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer origin.Close()
	if _, err := client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	go io.Copy(origin, client)
	_, _ = io.Copy(client, origin)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type erroringBody struct {
	read bool
}

func (b *erroringBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, errors.New("response body failed")
	}
	b.read = true
	return copy(p, "partial"), errors.New("response body failed")
}

func (b *erroringBody) Close() error { return nil }

func TestSendAbortsResponseCaptureOnReadError(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.cl.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &erroringBody{},
		}, nil
	})

	flow, err := snd.Send(Request{URL: "https://example.com/"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Flags&store.FlagCaptureError == 0 {
		t.Fatalf("expected capture error flag, got %d", flow.Flags)
	}
	err = filepath.WalkDir(s.BodiesDir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("temporary body file leaked: %s", d.Name())
		}
		return err
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestSendCapturesAsFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Test") != "1" {
			t.Errorf("missing custom header on upstream")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(201)
		io.WriteString(w, "echo:"+string(body))
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))

	flow, err := snd.Send(Request{
		Method:  "POST",
		URL:     upstream.URL + "/submit?x=1",
		Headers: map[string][]string{"X-Test": {"1"}},
		Body:    []byte("ping"),
		Flags:   store.FlagRepeater,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != 201 || flow.Method != "POST" || flow.Path != "/submit?x=1" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if flow.ReqLen != 4 {
		t.Fatalf("expected req len 4, got %d", flow.ReqLen)
	}
	if flow.Flags&store.FlagRepeater == 0 {
		t.Fatalf("expected FlagRepeater, flags=%d", flow.Flags)
	}

	rc, err := s.OpenBody(flow.ResBodyHash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "echo:ping" {
		t.Fatalf("response body mismatch: %q", b)
	}

	// Stored as a flow; RequireFlags surfaces it, ExcludeFlags hides it.
	if got, _ := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagRepeater}); len(got) != 1 {
		t.Fatalf("RequireFlags: expected 1, got %d", len(got))
	}
	if got, _ := s.QueryFlowsFilter(store.FlowFilter{ExcludeFlags: store.FlagRepeater}); len(got) != 0 {
		t.Fatalf("ExcludeFlags: expected 0, got %d", len(got))
	}
}

func TestSendHostOverridePreservesTargetURL(t *testing.T) {
	var gotHost, gotURI string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotURI = r.URL.RequestURI()
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))

	flow, err := snd.Send(Request{
		Method: http.MethodGet,
		URL:    target.URL + "/original?probe=1",
		Host:   "injection.example",
		Flags:  store.FlagRepeater,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != http.StatusOK {
		t.Fatalf("flow status=%d error=%q", flow.Status, flow.Error)
	}
	if gotHost != "injection.example" {
		t.Fatalf("wire Host=%q, want injection.example", gotHost)
	}
	if gotURI != "/original?probe=1" {
		t.Fatalf("target URI=%q, want /original?probe=1", gotURI)
	}
	if flow.Host != "127.0.0.1" || flow.Path != "/original?probe=1" {
		t.Fatalf("recorded target=%s%s, want original target URL", flow.Host, flow.Path)
	}
	if got := flow.ReqHeaders["Host"]; len(got) != 1 || got[0] != "injection.example" {
		t.Fatalf("recorded Host header=%v, want injection.example", got)
	}
}

func TestSessionHeadersInjected(t *testing.T) {
	var gotAuth, gotCookie, gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		gotHost = r.Host
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, _ := store.Open(t.TempDir())
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return true })

	// Off by default: nothing injected.
	snd.Send(Request{Method: "GET", URL: upstream.URL + "/a"})
	if gotAuth != "" {
		t.Fatalf("session off should not inject, got %q", gotAuth)
	}

	// Enabled: auth + cookie auto-applied even though the request omits them.
	snd.SetSession(true, []Header{
		{Key: "Authorization", Value: "Bearer T0KEN"},
		{Key: "Cookie", Value: "sid=abc"},
	})
	flow, err := snd.Send(Request{Method: "GET", URL: upstream.URL + "/b"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer T0KEN" || gotCookie != "sid=abc" {
		t.Fatalf("session headers not injected: auth=%q cookie=%q", gotAuth, gotCookie)
	}
	// Injected headers are recorded on the flow (transparency/repeatability).
	if got := flow.ReqHeaders["Authorization"]; len(got) == 0 || got[0] != "Bearer T0KEN" {
		t.Fatalf("injected header not recorded on flow: %v", flow.ReqHeaders)
	}

	// Session value replaces a stale explicit one (keeps sends authenticated).
	snd.Send(Request{Method: "GET", URL: upstream.URL + "/c", Headers: map[string][]string{"Authorization": {"Bearer STALE"}}})
	if gotAuth != "Bearer T0KEN" {
		t.Fatalf("session should override stale header, got %q", gotAuth)
	}

	// A Host entry rewrites the request Host.
	snd.SetSession(true, []Header{{Key: "Host", Value: "internal.test"}})
	snd.Send(Request{Method: "GET", URL: upstream.URL + "/d"})
	if gotHost != "internal.test" {
		t.Fatalf("session Host not applied, got %q", gotHost)
	}

	// Disabling stops injection.
	snd.SetSession(false, nil)
	gotAuth = ""
	snd.Send(Request{Method: "GET", URL: upstream.URL + "/e"})
	if gotAuth != "" {
		t.Fatalf("disabled session still injected: %q", gotAuth)
	}
}

func TestSessionHeadersScopeGated(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, _ := store.Open(t.TempDir())
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(host, scheme string, port int, path string) bool {
		return strings.HasPrefix(host, "127.0.0.1")
	})
	snd.SetSession(true, []Header{{Key: "Authorization", Value: "Bearer scoped"}})

	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return false })
	_, _ = snd.Send(Request{Method: "GET", URL: upstream.URL + "/out"})
	if gotAuth != "" {
		t.Fatalf("out-of-scope should not inject, got %q", gotAuth)
	}

	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return true })
	gotAuth = ""
	_, _ = snd.Send(Request{Method: "GET", URL: upstream.URL + "/in"})
	if gotAuth != "Bearer scoped" {
		t.Fatalf("in-scope should inject, got %q", gotAuth)
	}
}

func TestSessionHostHeadersOverride(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, _ := store.Open(t.TempDir())
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return true })
	snd.SetSession(true, []Header{{Key: "Authorization", Value: "Bearer global"}})

	// Per-host override for the test server replaces the global token.
	hostname := strings.Split(strings.TrimPrefix(upstream.URL, "http://"), ":")[0]
	snd.SetSessionHostHeaders(map[string][]Header{
		hostname: {{Key: "Authorization", Value: "Bearer per-host"}},
	})
	_, _ = snd.Send(Request{Method: "GET", URL: upstream.URL + "/a"})
	if gotAuth != "Bearer per-host" {
		t.Fatalf("expected per-host auth, got %q", gotAuth)
	}

	// Clearing per-host overrides falls back to global.
	snd.SetSessionHostHeaders(nil)
	_, _ = snd.Send(Request{Method: "GET", URL: upstream.URL + "/b"})
	if gotAuth != "Bearer global" {
		t.Fatalf("expected global auth after clearing, got %q", gotAuth)
	}
}

func TestSendRecordsUpstreamError(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))

	flow, err := snd.Send(Request{Method: "GET", URL: "http://127.0.0.1:1/nope", Flags: store.FlagRepeater})
	if err != nil {
		t.Fatalf("Send should record errors, not return them: %v", err)
	}
	if flow.Error == "" || flow.Status != http.StatusBadGateway {
		t.Fatalf("expected errored flow, got %+v", flow)
	}
}

func TestSendRejectsBadURL(t *testing.T) {
	s, _ := store.Open(t.TempDir())
	defer s.Close()
	snd := New(s, capture.New(s))
	if _, err := snd.Send(Request{Method: "GET", URL: "notaurl"}); err == nil {
		t.Fatal("expected error for non-absolute URL")
	}
}

func TestMacroInjectsFreshToken(t *testing.T) {
	// Token server hands out a rotating CSRF token in the body.
	var n int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		io.WriteString(w, `{"csrf":"tok-`+itoa(n)+`"}`)
	}))
	defer tokenSrv.Close()

	var gotHeader string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-CSRF-Token")
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return true })
	snd.SetMacro(Macro{
		Enabled:    true,
		Target:     tokenSrv.URL,
		Request:    "GET /token HTTP/1.1\nHost: t\n\n",
		Extract:    `"csrf":"([^"]+)"`,
		InjectMode: "header",
		InjectName: "X-CSRF-Token",
	})

	if _, err := snd.Send(Request{Method: "GET", URL: target.URL + "/x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotHeader == "" || gotHeader[:4] != "tok-" {
		t.Fatalf("expected a fresh macro token header, got %q", gotHeader)
	}
}

func TestSendAndSetSessionScopeConcurrent(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.cl.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			snd.SetSessionScope(func(string, string, int, string) bool { return true })
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			if _, err := snd.Send(Request{URL: "https://example.com/"}); err != nil {
				t.Errorf("Send: %v", err)
			}
		}
	}()
	wg.Wait()
}

func TestSessionHostHeadersDoNotRetainCallerMutation(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(string, string, int, string) bool { return true })
	snd.SetSession(true, nil)

	headers := map[string][]Header{"example.com": {{Key: "Authorization", Value: "Bearer original"}}}
	snd.SetSessionHostHeaders(headers)
	headers["example.com"][0].Value = "Bearer mutated"
	headers["other.example"] = []Header{{Key: "Authorization", Value: "Bearer added"}}

	var gotAuth string
	snd.cl.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	if _, err := snd.Send(Request{URL: "https://example.com/"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer original" {
		t.Fatalf("Authorization=%q; want original copied value", gotAuth)
	}
}

func TestSendDoesNotRaceCallerHostHeaderMutation(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(string, string, int, string) bool { return true })
	snd.SetSession(true, nil)
	headers := map[string][]Header{"example.com": {{Key: "Authorization", Value: "Bearer original"}}}
	snd.SetSessionHostHeaders(headers)
	snd.cl.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			headers["example.com"][0].Value = "Bearer changed"
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			if _, err := snd.Send(Request{URL: "https://example.com/"}); err != nil {
				t.Errorf("Send: %v", err)
			}
		}
	}()
	wg.Wait()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
