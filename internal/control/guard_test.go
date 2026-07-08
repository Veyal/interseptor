package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
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

	// Blocked: DNS-rebinding (foreign Host) is now unauthorized (401) — a valid key
	// is required for any non-loopback surface — rather than a flat 403.
	if c := code("evil.com:9966", ""); c != http.StatusUnauthorized {
		t.Fatalf("non-loopback Host (DNS rebind) must require auth (401), got %d", c)
	}
	// Classic CSRF (foreign Origin) on a loopback Host is still a flat 403.
	if c := code("127.0.0.1:9966", "http://evil.com"); c != http.StatusForbidden {
		t.Fatalf("cross-origin (CSRF) must be blocked, got %d", c)
	}
	if c := code("127.0.0.1:9966", "null"); c != http.StatusForbidden {
		t.Fatalf("opaque 'null' Origin must be blocked, got %d", c)
	}
}

// The loopback guard must not be defeatable by a spoofed Host header when the
// connection actually arrived on a non-loopback local interface (i.e. the
// control plane is bound to a routable address). The connection's server-side
// local address, unlike Host, cannot be spoofed by a remote client.
func TestSecurityGuardRejectsSpoofedHostOnNonLoopbackConn(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := (&Hub{}).securityGuard(next)

	codeWithLocalAddr := func(host, localAddr string) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9966/api/settings", nil)
		req.Host = host
		if localAddr != "" {
			ctx := context.WithValue(req.Context(), http.LocalAddrContextKey,
				&net.TCPAddr{IP: net.ParseIP(strings.Split(localAddr, ":")[0]), Port: 9966})
			req = req.WithContext(ctx)
		}
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		return rec.Code
	}

	// Spoofed loopback Host, but the connection landed on a routable local
	// address → rejected. Without a key this is now 401 (auth required) — still
	// firmly blocked, and the connection can't reach any handler.
	if c := codeWithLocalAddr("127.0.0.1:9966", "192.168.1.10:9966"); c != http.StatusUnauthorized {
		t.Fatalf("spoofed Host on non-loopback local addr must be blocked (401), got %d", c)
	}
	if c := codeWithLocalAddr("localhost:9966", "10.0.0.5:9966"); c != http.StatusUnauthorized {
		t.Fatalf("spoofed localhost Host on LAN local addr must be blocked (401), got %d", c)
	}

	// Genuine loopback connection with loopback Host → allowed (UI keeps working).
	if c := codeWithLocalAddr("127.0.0.1:9966", "127.0.0.1:9966"); c != http.StatusNoContent {
		t.Fatalf("genuine loopback connection must pass, got %d", c)
	}
}

// A guard rejection must use the same JSON {"error":…} shape as every other
// handler, so the UI's api() wrapper surfaces the explanatory message instead of
// a generic "Forbidden".
func TestSecurityGuardErrorShapeIsJSON(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := (&Hub{}).securityGuard(next)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9966/api/settings", nil)
	req.Host = "evil.com:9966"
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("guard error Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("guard error body not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.Error == "" {
		t.Fatalf("guard error body has empty error field: %q", rec.Body.String())
	}
}

// Every control request body is bounded by maxRequestBody (DoS backstop). An
// oversized body is rejected; a normal small body still works.
func TestControlBodySizeCapped(t *testing.T) {
	old := maxRequestBody
	maxRequestBody = 1 << 10 // 1 KiB for the test
	defer func() { maxRequestBody = old }()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(body string) int {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/notes", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if c := put(`{"notes":"` + strings.Repeat("a", 4096) + `"}`); c == http.StatusNoContent {
		t.Fatal("oversized body should be rejected, got 204")
	}

	maxRequestBody = old // restore: a normal body succeeds (204)
	if c := put(`{"notes":"hi"}`); c != http.StatusNoContent {
		t.Fatalf("small body should succeed (204), got %d", c)
	}
}

// Repeater/Intruder/WS must refuse a target that points at Interseptor's own
// control listener, so the tool can't be coerced into attacking its own API.
func TestSendRefusesOwnListener(t *testing.T) {
	h, _, _ := newHub(t)
	h.SetSelfAddr("127.0.0.1:9966")
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(path, body string) int {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if c := post("/api/repeater/send", `{"method":"GET","url":"http://127.0.0.1:9966/api/keys"}`); c != http.StatusForbidden {
		t.Fatalf("repeater → own listener: got %d, want 403", c)
	}
	if c := post("/api/ws/send", `{"url":"ws://127.0.0.1:9966/api/events"}`); c != http.StatusForbidden {
		t.Fatalf("ws → own listener: got %d, want 403", c)
	}
	if c := post("/api/intruder/start", `{"target":"http://localhost:9966/x","template":"GET / HTTP/1.1\r\nHost: x\r\n\r\n","attackType":"null","repeat":1}`); c != http.StatusForbidden {
		t.Fatalf("intruder → own listener: got %d, want 403", c)
	}
}

// A match/replace rule with an absurdly long regex is rejected before it reaches
// the engine (run on every proxied request).
func TestRuleRejectsOverlongPattern(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	long := strings.Repeat("a", maxRulePattern+1)
	resp, err := http.Post(ts.URL+"/api/rules", "application/json",
		strings.NewReader(`{"type":"req-header","match":"`+long+`","replace":""}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("overlong rule pattern: got %d, want 400", resp.StatusCode)
	}
}

// A valid API key authorizes a NON-loopback request (the remote-access path),
// with scope enforced: a full key may mutate, a read-only key may not, and the
// query-token form is only honored for the SSE stream.
func TestSecurityGuardKeyAuth(t *testing.T) {
	h, st, _ := newHub(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	g := h.securityGuard(next)

	full, _, err := st.CreateAPIKey("agent", store.ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey full: %v", err)
	}
	read, _, err := st.CreateAPIKey("viewer", store.ScopeRead, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey read: %v", err)
	}

	// Simulate a genuine non-loopback (tunnel) connection with a tunnel Host.
	req := func(method, path, bearer, cookie, query string, csrf bool) *http.Request {
		url := "http://x.trycloudflare.com" + path
		if query != "" {
			url += "?" + query
		}
		r := httptest.NewRequest(method, url, nil)
		r.Host = "x.trycloudflare.com"
		ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("10.0.0.9"), Port: 9966})
		r = r.WithContext(ctx)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		if csrf {
			r.Header.Set("X-Interseptor-CSRF", "1")
		}
		return r
	}
	code := func(r *http.Request) int {
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, r)
		return rec.Code
	}

	// No key on a remote connection → 401.
	if c := code(req(http.MethodGet, "/api/flows", "", "", "", false)); c != http.StatusUnauthorized {
		t.Fatalf("remote no key: want 401, got %d", c)
	}
	// Full key, bearer → GET and mutating both pass.
	if c := code(req(http.MethodGet, "/api/flows", full, "", "", false)); c != http.StatusNoContent {
		t.Fatalf("full key GET: want 204, got %d", c)
	}
	if c := code(req(http.MethodPost, "/api/scope", full, "", "", false)); c != http.StatusNoContent {
		t.Fatalf("full key POST (bearer): want 204, got %d", c)
	}
	// Read key → GET passes, mutating is 403.
	if c := code(req(http.MethodGet, "/api/flows", read, "", "", false)); c != http.StatusNoContent {
		t.Fatalf("read key GET: want 204, got %d", c)
	}
	if c := code(req(http.MethodPost, "/api/scope", read, "", "", false)); c != http.StatusForbidden {
		t.Fatalf("read key POST: want 403, got %d", c)
	}
	// Cookie-authed mutation requires the CSRF header.
	if c := code(req(http.MethodPost, "/api/scope", "", full, "", false)); c != http.StatusForbidden {
		t.Fatalf("cookie POST without CSRF header: want 403, got %d", c)
	}
	if c := code(req(http.MethodPost, "/api/scope", "", full, "", true)); c != http.StatusNoContent {
		t.Fatalf("cookie POST with CSRF header: want 204, got %d", c)
	}
	// A ?token= query is only honored for the SSE stream.
	if c := code(req(http.MethodGet, "/api/events", "", "", "token="+full, false)); c != http.StatusNoContent {
		t.Fatalf("query token on /api/events: want 204, got %d", c)
	}
	if c := code(req(http.MethodGet, "/api/flows", "", "", "token="+full, false)); c != http.StatusForbidden {
		t.Fatalf("query token off /api/events: want 403, got %d", c)
	}
	// /mcp requires a full-scope key even for a valid read key.
	if c := code(req(http.MethodPost, "/mcp", read, "", "", false)); c != http.StatusForbidden {
		t.Fatalf("read key on /mcp: want 403, got %d", c)
	}
	if c := code(req(http.MethodPost, "/mcp", full, "", "", false)); c == http.StatusUnauthorized || c == http.StatusForbidden {
		t.Fatalf("full key on /mcp must be authorized by the guard, got %d", c)
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

func TestPutSettingsRejectsExternalControlBindWhenLocked(t *testing.T) {
	t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", "0")
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(body string) int {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if c := put(`{"controlAddr":"0.0.0.0:9966"}`); c != http.StatusBadRequest {
		t.Fatalf("external control bind 0.0.0.0 must be rejected when locked, got %d", c)
	}
	if c := put(`{"controlAddr":"192.168.1.5:9966"}`); c != http.StatusBadRequest {
		t.Fatalf("LAN control bind must be rejected when locked, got %d", c)
	}
}

func TestPutSettingsRebindsControlAddr(t *testing.T) {
	h, st, _ := newHub(t)
	fake := &fakeRebinder{addrs: []string{"127.0.0.1:9966"}}
	h.SetControlRebinder(fake)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(`{"controlAddr":"127.0.0.1:9967"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, readAll(resp.Body))
	}
	if fake.Addr() != "127.0.0.1:9967" {
		t.Fatalf("rebind addr = %q, want 127.0.0.1:9967", fake.Addr())
	}
	if v, ok, _ := st.GetSetting("control.addr"); !ok || v != "127.0.0.1:9967" {
		t.Fatalf("persisted control.addr = %q, ok=%v", v, ok)
	}
	if h.GetSelfAddr() != "127.0.0.1:9967" {
		t.Fatalf("SelfAddr = %q", h.GetSelfAddr())
	}
}

type fakeRebinder struct{ addrs []string }

func (f *fakeRebinder) Rebind(addr string) error {
	f.addrs = []string{addr}
	return nil
}
func (f *fakeRebinder) RebindAddrs(addrs []string) error {
	f.addrs = append([]string(nil), addrs...)
	return nil
}
func (f *fakeRebinder) Addr() string {
	if len(f.addrs) == 0 {
		return ""
	}
	if len(f.addrs) == 1 {
		return f.addrs[0]
	}
	return strings.Join(f.addrs, ", ")
}
func (f *fakeRebinder) Addrs() []string {
	return append([]string(nil), f.addrs...)
}

func TestPutSettingsRejectsExternalBindWhenLocked(t *testing.T) {
	t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", "0")
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
		t.Fatalf("external bind 0.0.0.0 must be rejected when locked, got %d", c)
	}
	if c := put("192.168.1.5:8080"); c != http.StatusBadRequest {
		t.Fatalf("LAN bind must be rejected when locked, got %d", c)
	}
}

func TestPutSettingsAllowsExternalBindByDefault(t *testing.T) {
	t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", "")
	h, _, _ := newHub(t)
	reb := &fakeRebinder{}
	h.rebind = reb
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(`{"proxyAddr":"0.0.0.0:8080"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("external bind 0.0.0.0 should be allowed by default, got %d", resp.StatusCode)
	}
	if reb.Addr() != "0.0.0.0:8080" {
		t.Fatalf("rebind addr = %q", reb.Addr())
	}
}

func TestPutSettingsRebindsMultipleProxyAddrs(t *testing.T) {
	h, st, _ := newHub(t)
	reb := &fakeRebinder{addrs: []string{"127.0.0.1:8080"}}
	h.rebind = reb
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"proxyAddrs":["127.0.0.1:8080","0.0.0.0:8080"]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, readAll(resp.Body))
	}
	if !proxyAddrsEqual(reb.Addrs(), []string{"127.0.0.1:8080", "0.0.0.0:8080"}) {
		t.Fatalf("rebind addrs = %v", reb.Addrs())
	}
	if v, ok, _ := st.GetSetting("proxy.addrs"); !ok || !strings.Contains(v, "0.0.0.0:8080") {
		t.Fatalf("persisted proxy.addrs = %q, ok=%v", v, ok)
	}
	var out settingsJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.ProxyAddrs) != 2 {
		t.Fatalf("response proxyAddrs = %v", out.ProxyAddrs)
	}
}

func TestGetNetworkHosts(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/network/hosts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Hosts     []struct{ Address string } `json:"hosts"`
		Suggested string                      `json:"suggested"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hosts) < 4 {
		t.Fatalf("expected baseline hosts, got %d", len(out.Hosts))
	}
	if out.Suggested == "" {
		t.Fatal("missing suggested host")
	}
}
