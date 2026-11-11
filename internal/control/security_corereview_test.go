package control

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An IPv6 peer arrives as "[addr]:port". remoteIP kept the brackets, so the
// allowlist (net.ParseIP) never matched a directly connected IPv6 client and
// "Allow this IP" offered a value the allowlist then refused to store.
func TestRemoteIPStripsIPv6BracketsAndZone(t *testing.T) {
	for addr, want := range map[string]string{
		"[fd7a:115c::5]:4444":   "fd7a:115c::5",
		"[fe80::1%en0]:4444":    "fe80::1",
		"192.0.2.7:51000":       "192.0.2.7",
		"[::1]:9966":            "::1",
		"192.0.2.7":             "192.0.2.7",
		"[fd7a:115c::5]":        "fd7a:115c::5",
		"[::ffff:192.0.2.7]:80": "::ffff:192.0.2.7",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		r.RemoteAddr = addr
		if got := remoteIP(r); got != want {
			t.Errorf("remoteIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestAllowlistAdmitsDirectIPv6Client(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.AddIPAllowlist("fd7a:115c::5", "laptop"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := h.securityGuard(next)

	req := httptest.NewRequest(http.MethodGet, "http://[fd7a:115c::1]:9966/api/version", nil)
	req.Host = "[fd7a:115c::1]:9966"
	req.RemoteAddr = "[fd7a:115c::5]:4444"
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("fd7a:115c::1"), Port: 9966})
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowlisted IPv6 client: status %d body %s, want 204", rec.Code, rec.Body.String())
	}

	// A different IPv6 peer stays locked out.
	other := httptest.NewRequest(http.MethodGet, "http://[fd7a:115c::1]:9966/api/version", nil)
	other.Host = "[fd7a:115c::1]:9966"
	other.RemoteAddr = "[fd7a:115c::6]:4444"
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, other.WithContext(ctx))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-allowlisted IPv6 client: status %d, want 401", rec.Code)
	}
}
