package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

func TestIOSProfileEndpoint(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsca.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), ca, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ios/profile.mobileconfig?host=127.0.0.1&port=8080")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "apple-aspen-config") {
		t.Fatalf("content-type %q", ct)
	}
}

// When no iOS device is connected (the common first-run case),
// ios.ResolveDevice fails inside iosDeviceAndPort before deviceProxyHostPort
// is ever reached, so postIOSSetup's no-device manual-setup fallback used to
// reuse the zero-value port from that early-return error, producing a
// non-functional profile URL/proxy string like "127.0.0.1:0" while still
// returning 200 ok:true. The fallback must recompute the real configured
// proxy port independently of whether a device was found.
func TestIOSSetupNoDeviceReturnsRealPort(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsca.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), ca, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	// No udid → no device on a host with no simctl/idevice tooling → the
	// no-device manual-setup fallback path.
	resp, err := http.Post(ts.URL+"/api/ios/setup", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		OK         bool   `json:"ok"`
		ProfileURL string `json:"profileUrl"`
		Proxy      string `json:"proxy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("ok = false, want true")
	}
	if strings.Contains(out.Proxy, ":0") || strings.HasSuffix(out.Proxy, ":0") {
		t.Fatalf("proxy = %q, contains zero port (expected the real configured proxy port, e.g. 127.0.0.1:8080)", out.Proxy)
	}
	if strings.Contains(out.ProfileURL, "port=0") {
		t.Fatalf("profileUrl = %q, contains port=0 (expected the real configured proxy port)", out.ProfileURL)
	}
	if !strings.Contains(out.ProfileURL, "port=8080") {
		t.Fatalf("profileUrl = %q, want it to contain the default configured port 8080", out.ProfileURL)
	}
	if out.Proxy != "127.0.0.1:8080" {
		t.Fatalf("proxy = %q, want 127.0.0.1:8080", out.Proxy)
	}
}

func TestIOSSSHStatusProbeRequiresFullAuthorizationBeforeDial(t *testing.T) {
	// Given
	hub, st, _ := newHub(t)
	readToken, _, err := st.CreateAPIKey("viewer", store.ScopeRead, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	dials := 0
	hub.iosTCPReachable = func(string, int) bool {
		dials++
		return true
	}
	req := httptest.NewRequest(http.MethodGet, "http://remote.example/api/ios/ssh/status?host=192.0.2.1&port=22", nil)
	req.Host = "remote.example"
	req.Header.Set("Authorization", "Bearer "+readToken)

	// When
	rec := httptest.NewRecorder()
	hub.Handler().ServeHTTP(rec, req)

	// Then
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if dials != 0 {
		t.Fatalf("reachability probes = %d, want 0", dials)
	}
}

func TestIOSSSHStatusMetadataAllowsReadAuthorization(t *testing.T) {
	// Given
	hub, st, _ := newHub(t)
	readToken, _, err := st.CreateAPIKey("viewer", store.ScopeRead, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	dials := 0
	hub.iosTCPReachable = func(string, int) bool {
		dials++
		return true
	}
	req := httptest.NewRequest(http.MethodGet, "http://remote.example/api/ios/ssh/status", nil)
	req.Host = "remote.example"
	req.Header.Set("Authorization", "Bearer "+readToken)

	// When
	rec := httptest.NewRecorder()
	hub.Handler().ServeHTTP(rec, req)

	// Then
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if dials != 0 {
		t.Fatalf("reachability probes = %d, want 0", dials)
	}
}

func TestIOSSSHStatusProbeRejectsInvalidPortBeforeDial(t *testing.T) {
	// Given
	hub, _, _ := newHub(t)
	dials := 0
	hub.iosTCPReachable = func(string, int) bool {
		dials++
		return true
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/ios/ssh/status?host=192.0.2.1&port=invalid", nil)

	// When
	rec := httptest.NewRecorder()
	hub.Handler().ServeHTTP(rec, req)

	// Then
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if dials != 0 {
		t.Fatalf("reachability probes = %d, want 0", dials)
	}
}

func TestIOSStatusEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ios/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
