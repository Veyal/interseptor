package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustsForwardedOnlyFromLoopbackPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9966/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "100.65.105.2, 10.0.0.1")
	if got := clientIP(req); got != "100.65.105.2" {
		t.Fatalf("xff from loopback peer: %q", got)
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:9966/", nil)
	req2.RemoteAddr = "203.0.113.9:9999"
	req2.Header.Set("X-Forwarded-For", "100.65.105.2")
	if got := clientIP(req2); got != "203.0.113.9" {
		t.Fatalf("spoofed xff ignored: %q", got)
	}
	req3 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9966/", nil)
	req3.RemoteAddr = "127.0.0.1:1"
	req3.Header.Set("CF-Connecting-IP", "1.2.3.4")
	if got := clientIP(req3); got != "1.2.3.4" {
		t.Fatalf("cf: %q", got)
	}
}

func TestSecurityGuardAllowlistBypassesKey(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.AddIPAllowlist("100.65.105.2", "laptop"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := h.securityGuard(next)

	req := httptest.NewRequest(http.MethodGet, "http://mini.tail283fa9.ts.net:8443/api/version", nil)
	req.Host = "mini.tail283fa9.ts.net:8443"
	req.RemoteAddr = "127.0.0.1:4444"
	req.Header.Set("X-Forwarded-For", "100.65.105.2")
	ctx := context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9966})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowlisted GET: %d %s", rec.Code, rec.Body.String())
	}

	// /mcp still requires a key
	reqM := httptest.NewRequest(http.MethodPost, "http://mini.tail283fa9.ts.net:8443/mcp", bytes.NewReader(nil))
	reqM.Host = "mini.tail283fa9.ts.net:8443"
	reqM.RemoteAddr = "127.0.0.1:4444"
	reqM.Header.Set("X-Forwarded-For", "100.65.105.2")
	reqM = reqM.WithContext(ctx)
	recM := httptest.NewRecorder()
	g.ServeHTTP(recM, reqM)
	if recM.Code != http.StatusUnauthorized {
		t.Fatalf("mcp should still require key, got %d", recM.Code)
	}
}

func TestAllowlistAPI(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]string{"cidr": "100.64.0.0/10", "label": "tailscale"})
	resp, err := http.Post(ts.URL+"/api/allowlist", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	listResp, err := http.Get(ts.URL + "/api/allowlist")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(listResp.Body).Decode(&got)
	entries, _ := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("got=%v", got)
	}
	if _, ok := got["clientIP"].(string); !ok {
		t.Fatalf("missing clientIP: %v", got)
	}
}
