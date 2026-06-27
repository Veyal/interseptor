package sender

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/store"
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
		go func() { defer wg.Done(); snd.maybeRefreshLogin() }()
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
