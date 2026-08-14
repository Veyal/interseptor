package control

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testCertificatePEM(t *testing.T) string {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))
}

// PUT /api/settings {captureScopeOnly:true} persists the choice, calls the wired
// proxy hook, and GET /api/settings reflects it.
func TestCaptureScopeOnlySetting(t *testing.T) {
	h, _, _ := newHub(t)
	var got, called bool
	h.SetCaptureScopeOnly = func(v bool) { got, called = v, true }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"captureScopeOnly": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	if !called || !got {
		t.Fatalf("SetCaptureScopeOnly called=%v got=%v, want true/true", called, got)
	}

	gresp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()
	var s map[string]any
	json.NewDecoder(gresp.Body).Decode(&s)
	if s["captureScopeOnly"] != true {
		t.Fatalf("getSettings captureScopeOnly = %v, want true", s["captureScopeOnly"])
	}
}

// PUT /api/settings {invisibleProxy:true} persists the choice, calls the wired
// proxy hook, and GET /api/settings reflects it.
func TestInvisibleProxySetting(t *testing.T) {
	h, _, _ := newHub(t)
	var got, called bool
	h.SetInvisibleProxy = func(v bool) { got, called = v, true }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"invisibleProxy": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	if !called || !got {
		t.Fatalf("SetInvisibleProxy called=%v got=%v, want true/true", called, got)
	}

	gresp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()
	var s map[string]any
	json.NewDecoder(gresp.Body).Decode(&s)
	if s["invisibleProxy"] != true {
		t.Fatalf("getSettings invisibleProxy = %v, want true", s["invisibleProxy"])
	}
}

// PUT /api/settings {tlsBypassHosts, autoBypassOnPinFailure} normalizes + persists
// the list, calls the wired proxy hooks, and GET reflects the choices.
func TestUpstreamProxyCASetting(t *testing.T) {
	h, st, _ := newHub(t)
	var got string
	h.SetUpstreamProxyCA = func(v []byte) error { got = string(v); return nil }
	want := testCertificatePEM(t)

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"upstreamProxyCA": "\n  " + want + "  "})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	want = strings.TrimSpace(want)
	if got != want {
		t.Fatalf("SetUpstreamProxyCA = %q, want %q", got, want)
	}
	stored, ok, err := st.GetSetting("upstream.proxyCA")
	if err != nil || !ok || stored != want {
		t.Fatalf("stored upstream CA = %q, %v, %v; want %q, true, nil", stored, ok, err, want)
	}

	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings["upstreamProxyCA"] != want {
		t.Fatalf("response upstreamProxyCA = %q, want %q", settings["upstreamProxyCA"], want)
	}
}

func TestUpstreamProxyCASettingRollsBackRuntimeOnPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	previous := strings.TrimSpace(testCertificatePEM(t))
	current := strings.TrimSpace(testCertificatePEM(t))
	if err := st.SetSetting("upstream.proxyCA", previous); err != nil {
		t.Fatal(err)
	}
	var applied []string
	h.SetUpstreamProxyCA = func(v []byte) error { applied = append(applied, string(v)); return nil }
	h.setSetting = func(string, string) error { return errors.New("store unavailable") }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body, _ := json.Marshal(map[string]string{"upstreamProxyCA": current})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("PUT settings status %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if len(applied) != 2 || applied[0] != current || applied[1] != previous {
		t.Fatalf("runtime CA applications did not apply current then previous")
	}
	stored, ok, err := st.GetSetting("upstream.proxyCA")
	if err != nil || !ok || stored != previous {
		t.Fatalf("stored upstream CA did not retain previous value: ok=%v err=%v", ok, err)
	}
}

func TestUpstreamProxyCASettingRejectsInvalidWithoutPersisting(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting("upstream.proxyCA", "existing"); err != nil {
		t.Fatal(err)
	}
	h.SetUpstreamProxyCA = func([]byte) error { return errors.New("invalid certificate") }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewBufferString(`{"upstreamProxyCA":"not a certificate"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT settings status %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	stored, ok, err := st.GetSetting("upstream.proxyCA")
	if err != nil || !ok || stored != "existing" {
		t.Fatalf("stored upstream CA = %q, %v, %v; want existing, true, nil", stored, ok, err)
	}
}

func TestTLSBypassSettings(t *testing.T) {
	h, _, _ := newHub(t)
	var gotHosts []string
	var gotAuto, autoCalled bool
	h.SetTLSBypassHosts = func(v []string) { gotHosts = v }
	h.SetAutoBypassOnPinFailure = func(v bool) { gotAuto, autoCalled = v, true }

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Duplicates / blanks / mixed case must be normalized on the way in.
	body, _ := json.Marshal(map[string]any{
		"tlsBypassHosts":         []string{" *.Pinned.com ", "pinned.com", "*.pinned.com", ""},
		"autoBypassOnPinFailure": true,
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT settings: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status %d", resp.StatusCode)
	}
	if len(gotHosts) != 2 { // "*.pinned.com" + "pinned.com", deduped/lowercased
		t.Fatalf("SetTLSBypassHosts got %v, want 2 normalized entries", gotHosts)
	}
	if !autoCalled || !gotAuto {
		t.Fatalf("SetAutoBypassOnPinFailure called=%v got=%v, want true/true", autoCalled, gotAuto)
	}

	gresp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()
	var s map[string]any
	json.NewDecoder(gresp.Body).Decode(&s)
	if s["autoBypassOnPinFailure"] != true {
		t.Fatalf("getSettings autoBypassOnPinFailure = %v, want true", s["autoBypassOnPinFailure"])
	}
	if hosts, _ := s["tlsBypassHosts"].([]any); len(hosts) != 2 {
		t.Fatalf("getSettings tlsBypassHosts = %v, want 2 entries", s["tlsBypassHosts"])
	}
}
