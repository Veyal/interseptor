package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityGuardHostAndOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := (&Hub{}).securityGuard(next)
	code := func(host, origin string) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9966/api/settings", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		return rec.Code
	}

	// Allowed: loopback Host, with no Origin or a loopback Origin.
	if c := code("127.0.0.1:9966", ""); c != http.StatusNoContent {
		t.Fatalf("loopback Host, no Origin should pass, got %d", c)
	}
	if c := code("localhost:9966", "http://localhost:9966"); c != http.StatusNoContent {
		t.Fatalf("same-origin should pass, got %d", c)
	}
	if c := code("[::1]:9966", ""); c != http.StatusNoContent {
		t.Fatalf("ipv6 loopback should pass, got %d", c)
	}

	// Blocked: DNS-rebinding (foreign Host) and classic CSRF (foreign Origin).
	if c := code("evil.com:9966", ""); c != http.StatusForbidden {
		t.Fatalf("non-loopback Host (DNS rebind) must be blocked, got %d", c)
	}
	if c := code("127.0.0.1:9966", "http://evil.com"); c != http.StatusForbidden {
		t.Fatalf("cross-origin (CSRF) must be blocked, got %d", c)
	}
	if c := code("127.0.0.1:9966", "null"); c != http.StatusForbidden {
		t.Fatalf("opaque 'null' Origin must be blocked, got %d", c)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1:9966", "localhost", "localhost:9966", "[::1]:9966", "127.0.0.1", "::1", "127.5.5.5:80"} {
		if !isLoopbackHost(h) {
			t.Errorf("%q should be loopback", h)
		}
	}
	for _, h := range []string{"", "0.0.0.0:8080", ":8080", "evil.com:9966", "10.0.0.5:8080", "169.254.1.1", "192.168.1.10"} {
		if isLoopbackHost(h) {
			t.Errorf("%q should NOT be loopback", h)
		}
	}
}

func TestPutSettingsRejectsExternalBind(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(addr string) int {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(`{"proxyAddr":"`+addr+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if c := put("0.0.0.0:8080"); c != http.StatusBadRequest {
		t.Fatalf("external bind 0.0.0.0 must be rejected, got %d", c)
	}
	if c := put("192.168.1.5:8080"); c != http.StatusBadRequest {
		t.Fatalf("LAN bind must be rejected, got %d", c)
	}
}
