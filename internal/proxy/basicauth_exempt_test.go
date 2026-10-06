package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuth_exemptPeerSkipsChallenge(t *testing.T) {
	auth := &BasicAuth{}
	auth.Set(true, "proxy", "secret")
	allowed := map[string]bool{"192.0.2.10": true}
	auth.SetExempt(func(ip string) bool { return allowed[ip] })
	var calls int
	handler := auth.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	build := func(kind, remote string) *http.Request {
		var r *http.Request
		switch kind {
		case "explicit":
			r = httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		case "origin":
			r = httptest.NewRequest(http.MethodGet, "/", nil)
			r.Host = "example.com"
		default:
			r = httptest.NewRequest(http.MethodConnect, "example.com:443", nil)
			r.Host = "example.com:443"
		}
		r.RemoteAddr = remote
		return r
	}
	for _, kind := range []string{"explicit", "origin", "connect"} {
		calls = 0
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, build(kind, "192.0.2.10:5555"))
		if rec.Code != http.StatusNoContent || calls != 1 {
			t.Errorf("%s allowlisted: status=%d calls=%d", kind, rec.Code, calls)
		}
		calls = 0
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, build(kind, "192.0.2.99:5555"))
		if rec.Code != http.StatusProxyAuthRequired || calls != 0 {
			t.Errorf("%s non-allowlisted: status=%d calls=%d", kind, rec.Code, calls)
		}
	}
	// Forwarding headers must never grant the exemption.
	r := build("explicit", "192.0.2.99:5555")
	r.Header.Set("X-Forwarded-For", "192.0.2.10")
	r.Header.Set("CF-Connecting-IP", "192.0.2.10")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusProxyAuthRequired {
		t.Errorf("spoofed XFF: status=%d, want 407", rec.Code)
	}
	// Clearing the matcher restores the challenge without rebuilding the handler.
	auth.SetExempt(nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, build("explicit", "192.0.2.10:5555"))
	if rec.Code != http.StatusProxyAuthRequired {
		t.Errorf("after SetExempt(nil): status=%d, want 407", rec.Code)
	}
}
