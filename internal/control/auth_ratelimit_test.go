package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: the login limiter keyed on the TCP peer, so behind a tunnel
// (every client arrives from 127.0.0.1) one client's failed attempts locked
// every other collaborator out of /login.
func TestLoginRateLimitIsPerForwardedClient(t *testing.T) {
	h, _, _ := newHub(t)
	saved := loginRL
	loginRL = &loginRateLimiter{window: map[string]*rlEntry{}}
	t.Cleanup(func() { loginRL = saved })
	attempt := func(client string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/session/auth", strings.NewReader(`{"token":"ick_wrong"}`))
		r.RemoteAddr = "127.0.0.1:5555"
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		h.sessionLogin(w, r)
		return w.Code
	}
	for i := 0; i < 10; i++ {
		attempt("203.0.113.7")
	}
	if c := attempt("203.0.113.7"); c != http.StatusTooManyRequests {
		t.Fatalf("11th attempt from one client = %d, want 429", c)
	}
	if c := attempt("198.51.100.4"); c != http.StatusUnauthorized {
		t.Fatalf("another tunnel client was locked out: %d, want 401", c)
	}
}
