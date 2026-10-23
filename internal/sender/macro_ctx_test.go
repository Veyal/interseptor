package sender

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

func newTestSender(t *testing.T) *Sender {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	snd := New(s, capture.New(s))
	snd.SetSessionScope(func(string, string, int, string) bool { return true })
	return snd
}

// A token/login macro endpoint that never answers must not outlive the send's
// context: cancelling the send returns promptly.
func TestStalledMacrosHonourSendContext(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/macro" || r.URL.Path == "/login" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	defer close(release)

	for name, setup := range map[string]func(*Sender){
		"token macro": func(s *Sender) {
			s.SetMacro(Macro{Enabled: true, Target: upstream.URL, Request: "GET /macro HTTP/1.1\r\nHost: x\r\n\r\n", Extract: "t=(\\w+)", InjectName: "X-T"})
		},
		"login macro": func(s *Sender) {
			_ = s.SetLoginMacro(LoginMacro{Enabled: true, RefreshSecs: 60, Target: upstream.URL, Request: "GET /login HTTP/1.1\r\nHost: x\r\n\r\n"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			snd := newTestSender(t)
			setup(snd)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			done := make(chan struct{})
			go func() {
				_, _ = snd.Send(Request{Method: "GET", URL: upstream.URL + "/api", Context: ctx})
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("send hung on a stalled macro endpoint despite a cancelled context")
			}
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("send took %v", d)
			}
		})
	}
}

// N concurrent sends that all get 401 must trigger a single login, and an
// existing session header unrelated to the login result must be kept.
func TestConcurrent401ReauthRunsOneLoginAndMergesHeaders(t *testing.T) {
	var logins atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			logins.Add(1)
			time.Sleep(50 * time.Millisecond)
			w.Header().Set("Set-Cookie", "sid=fresh; Path=/")
		case "/api":
			if r.Header.Get("Cookie") != "sid=fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
	}))
	defer upstream.Close()

	snd := newTestSender(t)
	_ = snd.SetLoginMacro(LoginMacro{Enabled: true, Target: upstream.URL, ReauthOn401: true, Request: "GET /login HTTP/1.1\r\nHost: x\r\n\r\n"})
	snd.SetSession(true, []Header{{Key: "X-Tenant", Value: "acme"}, {Key: "Cookie", Value: "sid=stale"}})

	const n = 20
	var wg sync.WaitGroup
	statuses := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := snd.Send(Request{Method: "GET", URL: upstream.URL + "/api"})
			if err == nil {
				statuses <- f.Status
			}
		}()
	}
	wg.Wait()
	close(statuses)
	for st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("a send ended with %d, want 200 after re-auth", st)
		}
	}
	if got := logins.Load(); got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}
	snd.sess.mu.RLock()
	defer snd.sess.mu.RUnlock()
	got := map[string]string{}
	for _, h := range snd.sess.headers {
		got[h.Key] = h.Value
	}
	if got["X-Tenant"] != "acme" || got["Cookie"] != "sid=fresh" || len(snd.sess.headers) != 2 {
		t.Fatalf("session headers after re-auth = %+v, want X-Tenant kept and Cookie replaced", snd.sess.headers)
	}
}

func TestMergeSessionHeaders(t *testing.T) {
	got := mergeSessionHeaders(
		[]Header{{Key: "cookie", Value: "a"}, {Key: "X-Keep", Value: "k"}},
		[]Header{{Key: "Cookie", Value: "b"}, {Key: "Authorization", Value: "Bearer t"}},
	)
	want := []Header{{Key: "Cookie", Value: "b"}, {Key: "X-Keep", Value: "k"}, {Key: "Authorization", Value: "Bearer t"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}
