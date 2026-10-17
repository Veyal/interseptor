package control

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression: behind a reverse proxy that appends to X-Forwarded-For, the
// client's own first hop was trusted, so "X-Forwarded-For: <allowlisted IP>"
// granted keyless full access. The hop the proxy appended (right-most) is the
// one to trust.
func TestClientIPUsesRightMostForwardedHop(t *testing.T) {
	h, _, _ := newHub(t)
	h.setTunnelHost("https://tunnel.example.com")
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "10.9.9.9, 203.0.113.7")
	if got := h.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want the proxy-appended hop 203.0.113.7", got)
	}
	// A forwarded header must not outrank the proxy-appended XFF hop.
	req.Header.Set("CF-Connecting-IP", "10.9.9.9")
	if got := h.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("clientIP with spoofed CF-Connecting-IP = %q, want 203.0.113.7", got)
	}
}

func allowlistGuardRequest(h *Hub, method, host, peer, local, xff string) int {
	g := h.securityGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(method, "http://"+host+"/api/keys", nil)
	req.Host = host
	req.RemoteAddr = peer
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://"+host)
	}
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(local), Port: 9966})
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req.WithContext(ctx))
	return rec.Code
}

func TestAllowlistRejectsSpoofedForwardedAndRebinding(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.AddIPAllowlist("10.9.9.9/32", "lab"); err != nil {
		t.Fatal(err)
	}
	h.setTunnelHost("https://tunnel.example.com")
	// Spoofed first hop through an appending tunnel: closed.
	if c := allowlistGuardRequest(h, http.MethodGet, "tunnel.example.com", "127.0.0.1:4000", "127.0.0.1", "10.9.9.9, 203.0.113.7"); c != http.StatusUnauthorized {
		t.Errorf("spoofed XFF through tunnel = %d, want 401", c)
	}
	// DNS rebinding: an allowlisted client's browser reaches the listener
	// directly under an attacker-controlled public name.
	if c := allowlistGuardRequest(h, http.MethodPost, "rebind.example.com:9966", "10.9.9.9:5000", "10.9.9.1", ""); c != http.StatusForbidden {
		t.Errorf("rebinding Host on a direct connection = %d, want 403", c)
	}
	// Legitimate direct access by IP, LAN or MagicDNS name keeps working.
	for _, host := range []string{"10.9.9.1:9966", "mini:9966", "mini.local:9966", "mini.tail283fa9.ts.net:9966", "[fd7a:115c::1]:9966"} {
		if c := allowlistGuardRequest(h, http.MethodPost, host, "10.9.9.9:5000", "10.9.9.1", ""); c != http.StatusNoContent {
			t.Errorf("allowlisted direct access via %s = %d, want 204", host, c)
		}
	}
	// The proxy-appended hop is still honoured for a genuine tunnel client.
	if c := allowlistGuardRequest(h, http.MethodGet, "tunnel.example.com", "127.0.0.1:4000", "127.0.0.1", "203.0.113.7, 10.9.9.9"); c != http.StatusNoContent {
		t.Errorf("tunnel client with appended allowlisted hop = %d, want 204", c)
	}
}

// Regression: a loopback peer was exempt from the rebinding Host check and its
// X-Forwarded-For was trusted, so a rebinding page in the operator's own
// browser (or any local process) could claim an allowlisted IP via XFF.
func TestAllowlistRejectsLoopbackRebindingWithForgedForwardedFor(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.AddIPAllowlist("10.9.9.9/32", "lab"); err != nil {
		t.Fatal(err)
	}
	if c := allowlistGuardRequest(h, http.MethodPost, "rebind.example.com:9966", "127.0.0.1:5000", "127.0.0.1", "10.9.9.9"); c != http.StatusUnauthorized && c != http.StatusForbidden {
		t.Fatalf("loopback rebinding with forged XFF = %d, want 401/403", c)
	}
	if c := allowlistGuardRequest(h, http.MethodGet, "rebind.example.com:9966", "127.0.0.1:5000", "127.0.0.1", "10.9.9.9"); c != http.StatusUnauthorized && c != http.StatusForbidden {
		t.Fatalf("loopback rebinding GET with forged XFF = %d, want 401/403", c)
	}
}
