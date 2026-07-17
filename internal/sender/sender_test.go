package sender

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

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
