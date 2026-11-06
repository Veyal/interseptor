package sender

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// Many concurrent sends hitting the refresh TTL at once must fire the login macro
// only ONCE (no thundering herd).
func TestMaybeRefreshLoginNoThunderingHerd(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			atomic.AddInt64(&hits, 1)
			time.Sleep(20 * time.Millisecond) // hold so concurrent callers pile up at the gate
			w.Header().Set("Set-Cookie", "sid=x; Path=/")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.cl = upstream.Client()
	snd.SetLoginMacro(LoginMacro{
		Enabled: true, RefreshSecs: 1, Target: upstream.URL,
		Request: "GET /login HTTP/1.1\r\nHost: example\r\n\r\n",
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); snd.maybeRefreshLogin(context.Background()) }()
	}
	wg.Wait()

	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("login macro should fire exactly once, fired %d times", n)
	}
}

func TestExtractSessionHeaders(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "sid=abc; Path=/; HttpOnly")
	resp.Header.Add("Set-Cookie", "csrf=xyz; Path=/")
	resp.Header.Add("Authorization", "Bearer tok")
	hdrs := ExtractSessionHeaders(resp)
	if len(hdrs) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(hdrs))
	}
	var cookie, auth string
	for _, h := range hdrs {
		switch h.Key {
		case "Cookie":
			cookie = h.Value
		case "Authorization":
			auth = h.Value
		}
	}
	if auth != "Bearer tok" {
		t.Fatalf("auth: %q", auth)
	}
	if cookie != "sid=abc; csrf=xyz" {
		t.Fatalf("cookie: %q", cookie)
	}
}

func TestSetLoginMacroRejectsUnsafeRefreshIntervals(t *testing.T) {
	snd := &Sender{}
	invalid := []int{-1}
	maxInt := int(^uint(0) >> 1)
	maxSafe := int64(^uint64(0)>>1) / int64(time.Second)
	if uint64(maxInt) > uint64(maxSafe) {
		invalid = append(invalid, maxInt)
	}
	for _, refreshSecs := range invalid {
		if err := snd.SetLoginMacro(LoginMacro{RefreshSecs: refreshSecs}); err == nil {
			t.Fatalf("SetLoginMacro(%d) succeeded, want validation error", refreshSecs)
		}
	}
}

func TestLoginRefreshPersistenceFailureKeepsExistingSession(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "sid=fresh; Path=/")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	snd := &Sender{cl: upstream.Client()}
	snd.SetSession(true, []Header{{Key: "Cookie", Value: "sid=old"}})
	if err := snd.SetLoginMacro(LoginMacro{
		Enabled: true, Target: upstream.URL,
		Request: "GET /login HTTP/1.1\r\nHost: example.com\r\n\r\n",
	}); err != nil {
		t.Fatal(err)
	}
	snd.SetSessionRefresh(func([]Header) error { return errors.New("persistence failed") })

	if _, err := snd.RunLoginMacroNow(); err == nil {
		t.Fatal("RunLoginMacroNow succeeded, want persistence error")
	}
	snd.sess.mu.RLock()
	defer snd.sess.mu.RUnlock()
	if got := snd.sess.headers; len(got) != 1 || got[0].Value != "sid=old" {
		t.Fatalf("live session changed after persistence failure: %+v", got)
	}
}

func TestRunLoginMacroExtractsSession(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		w.Header().Set("Set-Cookie", "session=logged-in; Path=/")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	cl := upstream.Client()
	hdrs, err := RunLoginMacro(cl, LoginMacro{
		Enabled: true,
		Target:  upstream.URL,
		Request: "POST /login HTTP/1.1\r\nHost: " + upstream.Listener.Addr().String() + "\r\nContent-Length: 0\r\n\r\n",
	})
	if err != nil {
		t.Fatalf("RunLoginMacro: %v", err)
	}
	if len(hdrs) != 1 || hdrs[0].Key != "Cookie" || hdrs[0].Value != "session=logged-in" {
		t.Fatalf("unexpected headers: %+v", hdrs)
	}
}

// TestLoginMacro must DRY-RUN: return the login response status + the session
// headers it would set, while leaving the live session untouched (so a subsequent
// send carries no session).
func TestTestLoginMacroIsDryRun(t *testing.T) {
	var sawCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Set-Cookie", "session=dry; Path=/")
			w.WriteHeader(201)
		case "/api":
			sawCookie = r.Header.Get("Cookie")
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.cl = upstream.Client()
	snd.SetSession(true, nil) // session enabled, no headers yet

	m := LoginMacro{Enabled: true, Target: upstream.URL,
		Request: "POST /login HTTP/1.1\r\nHost: " + upstream.Listener.Addr().String() + "\r\nContent-Length: 0\r\n\r\n"}
	status, hdrs, err := snd.TestLoginMacro(m)
	if err != nil {
		t.Fatalf("TestLoginMacro: %v", err)
	}
	if status != 201 {
		t.Fatalf("status: got %d want 201", status)
	}
	if len(hdrs) != 1 || hdrs[0].Key != "Cookie" || hdrs[0].Value != "session=dry" {
		t.Fatalf("headers: %+v", hdrs)
	}

	// The dry-run must not have applied the session: a live send carries no cookie.
	if _, err := snd.Send(Request{Method: "GET", URL: upstream.URL + "/api"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sawCookie != "" {
		t.Fatalf("dry-run leaked session into a live send: %q", sawCookie)
	}
}

func TestReauthOn401RetriesOnce(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Set-Cookie", "sid=fresh; Path=/")
			w.WriteHeader(200)
		case "/api":
			hits++
			if r.Header.Get("Cookie") != "sid=fresh" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(host, scheme string, port int, path string) bool { return true })
	snd.SetLoginMacro(LoginMacro{
		Enabled: true, Target: upstream.URL, ReauthOn401: true,
		Request: "GET /login HTTP/1.1\r\nHost: example\r\n\r\n",
	})
	snd.SetSession(true, nil)

	flow, err := snd.Send(Request{Method: "GET", URL: upstream.URL + "/api"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Status != 200 {
		t.Fatalf("expected 200 after re-auth, got %d (hits=%d)", flow.Status, hits)
	}
	if hits != 2 {
		t.Fatalf("expected 2 /api hits (401 + retry), got %d", hits)
	}
}

// The login response body is not used, but reading only the first megabyte of it
// left the rest unread, so net/http dropped the connection: every re-login
// against a large login page opened a new one. Draining keeps it reusable.
func TestLoginMacroDrainsLargeResponseForConnectionReuse(t *testing.T) {
	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = 'A'
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "sid=fresh; Path=/")
		_, _ = w.Write(big)
	}))
	var opened atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	snd := &Sender{cl: srv.Client()}
	if err := snd.SetLoginMacro(LoginMacro{Enabled: true, Target: srv.URL,
		Request: "GET /login HTTP/1.1\r\nHost: example.com\r\n\r\n"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := snd.RunLoginMacroNow(); err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("3 logins opened %d connections; the 2 MB login response was not drained", got)
	}
}

// The token macro keeps its 1 MB match window, but must drain the rest of the
// response: an unread remainder makes net/http drop the connection, so every
// send pays a fresh TCP+TLS handshake to the macro endpoint.
func TestMacroResponseDrainedForConnectionReuse(t *testing.T) {
	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = 'A'
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/macro" {
			io.WriteString(w, "tok=abc")
			_, _ = w.Write(big)
			return
		}
		io.WriteString(w, "ok")
	}))
	var opened atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	snd := newOptSender(t)
	snd.SetSessionScope(func(string, string, int, string) bool { return true })
	snd.SetMacro(Macro{Enabled: true, Target: srv.URL, Request: "GET /macro HTTP/1.1\r\nHost: x\r\n\r\n",
		Extract: `tok=(\w+)`, InjectMode: "header", InjectName: "X-Tok"})
	for i := 0; i < 3; i++ {
		if f, err := snd.Send(Request{URL: srv.URL + "/api"}); err != nil || f.Status != http.StatusOK {
			t.Fatalf("send %d: %v %+v", i, err, f)
		}
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("3 sends with a 2 MB macro response opened %d connections; the macro body was not drained", got)
	}
}
