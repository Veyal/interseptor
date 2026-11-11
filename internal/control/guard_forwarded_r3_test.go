package control

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func guardLoopbackReq(method, host string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest(method, "http://"+host+"/api/keys", strings.NewReader(`{"scope":"full"}`))
	req.Host = host
	req.RemoteAddr = "127.0.0.1:4444"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9966}))
}

// A local forwarder that routes by TLS SNI (Tailscale Serve/Funnel over HTTPS)
// or has a catch-all vhost relays the client's own Host header to our loopback
// socket. A remote client that sends "Host: localhost:9966" then looks exactly
// like the local operator: loopback socket, loopback Host. The forwarder's own
// headers are the only thing that tells the two apart, so a request carrying
// them must not take the keyless loopback path.
func TestSecurityGuardForwardedRequestIsNotLoopbackTrusted(t *testing.T) {
	h, _, _ := newHub(t)
	reached := false
	g := h.securityGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, hdr := range []map[string]string{
		{"X-Forwarded-For": "100.100.1.1", "X-Forwarded-Host": "localhost:9966"},
		{"X-Forwarded-For": "203.0.113.9"},
		{"X-Forwarded-Host": "127.0.0.1:9966"},
		{"Forwarded": "for=203.0.113.9"},
		{"X-Real-IP": "203.0.113.9"},
		{"CF-Connecting-IP": "203.0.113.9"},
		{"Tailscale-User-Login": "someone@example.com"},
	} {
		for _, host := range []string{"localhost:9966", "127.0.0.1:9966", "[::1]:9966"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				reached = false
				rec := httptest.NewRecorder()
				g.ServeHTTP(rec, guardLoopbackReq(method, host, hdr))
				if reached || rec.Code != http.StatusUnauthorized {
					t.Errorf("%s Host %s with %v: status %d reached=%v, want 401 (a relayed request got keyless loopback trust)", method, host, hdr, rec.Code, reached)
				}
			}
		}
	}
	// The plain local request still needs no key.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, guardLoopbackReq(http.MethodPost, "127.0.0.1:9966", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("plain loopback request: %d %s", rec.Code, rec.Body.String())
	}
}

// A relayed request with a valid key is still served: the key path does not
// depend on where the request came from.
func TestSecurityGuardForwardedRequestWithKeyStillWorks(t *testing.T) {
	h, st, _ := newHub(t)
	tok, _, err := st.CreateAPIKey("r3", "full", 0)
	if err != nil {
		t.Fatal(err)
	}
	g := h.securityGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, guardLoopbackReq(http.MethodPost, "localhost:9966", map[string]string{
		"X-Forwarded-For": "100.100.1.1", "Authorization": "Bearer " + tok,
	}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("forwarded request with a full key: %d %s", rec.Code, rec.Body.String())
	}
}

// End to end, with a real reverse proxy in front of the control listener that
// behaves like Tailscale Serve over HTTPS: it routes by something other than
// the Host header (SNI there, a fixed target here), passes the client's Host
// through unchanged and adds X-Forwarded-For. A remote client that names
// localhost in Host must not reach the API without a key.
func TestHostPreservingForwarderCannotBorrowLoopbackTrust(t *testing.T) {
	h, st, _ := newHub(t)
	control := httptest.NewServer(h.Handler()) // listens on 127.0.0.1
	defer control.Close()
	target, _ := url.Parse(control.URL)
	forwarder := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = r.In.Host
		r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
		r.Out.Header.Set("X-Forwarded-For", "100.100.1.1")
	}})
	defer forwarder.Close()

	do := func(method, path, host string, hdr map[string]string) int {
		req, _ := http.NewRequest(method, forwarder.URL+path, strings.NewReader(`{"label":"x","scope":"full"}`))
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, host := range []string{"localhost:" + target.Port(), "127.0.0.1:" + target.Port()} {
		if code := do(http.MethodGet, "/api/flows", host, nil); code != http.StatusUnauthorized {
			t.Errorf("relayed GET /api/flows with Host %s: %d, want 401", host, code)
		}
		if code := do(http.MethodPost, "/api/keys", host, nil); code != http.StatusUnauthorized {
			t.Errorf("relayed POST /api/keys with Host %s: %d, want 401 (a remote client minted itself an API key)", host, code)
		}
	}
	if keys, _ := st.ListAPIKeys(); len(keys) != 0 {
		t.Fatalf("%d API key(s) were created through the forwarder without authentication", len(keys))
	}
	// Direct local access is unchanged.
	resp, err := http.Get(control.URL + "/api/flows")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct loopback GET: %d", resp.StatusCode)
	}
}
