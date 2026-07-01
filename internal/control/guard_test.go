package control

import (
	"encoding/json"
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

// Repeater/Intruder/WS must refuse a target that points at Interceptor's own
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
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "0")
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
	fake := &fakeRebinder{addr: "127.0.0.1:9966"}
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
	if fake.addr != "127.0.0.1:9967" {
		t.Fatalf("rebind addr = %q, want 127.0.0.1:9967", fake.addr)
	}
	if v, ok, _ := st.GetSetting("control.addr"); !ok || v != "127.0.0.1:9967" {
		t.Fatalf("persisted control.addr = %q, ok=%v", v, ok)
	}
	if h.GetSelfAddr() != "127.0.0.1:9967" {
		t.Fatalf("SelfAddr = %q", h.GetSelfAddr())
	}
}

type fakeRebinder struct{ addr string }

func (f *fakeRebinder) Rebind(addr string) error { f.addr = addr; return nil }
func (f *fakeRebinder) Addr() string             { return f.addr }

func TestPutSettingsRejectsExternalBindWhenLocked(t *testing.T) {
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "0")
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
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "")
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
	if reb.addr != "0.0.0.0:8080" {
		t.Fatalf("rebind addr = %q", reb.addr)
	}
}
