package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemProxyRejectsTrailingJSONBeforeHostMutation(t *testing.T) {
	h, _, _ := newHub(t)
	called := false
	h.sysProxyEnable = func(string, int) error {
		called = true
		return nil
	}
	h.sysProxyDisable = func() error {
		called = true
		return nil
	}
	h.sysProxyStatus = func() (bool, error) { return false, nil }
	h.sysProxySupported = func() bool { return true }
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/sysproxy", "application/json",
		strings.NewReader(`{"enabled":true}{}`))
	if err != nil {
		t.Fatalf("POST system proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if called {
		t.Fatal("system-proxy host operation ran after rejected command")
	}
}

// The UI renders the POST response, so it must carry the state after the toggle.
// A fake backend stands in for networksetup; the real system proxy is never touched.
func TestSystemProxyToggleResponseReportsNewState(t *testing.T) {
	h, _, _ := newHub(t)
	on := false
	h.sysProxyEnable = func(string, int) error { on = true; return nil }
	h.sysProxyDisable = func() error { on = false; return nil }
	h.sysProxyStatus = func() (bool, error) { return on, nil }
	h.sysProxySupported = func() bool { return true }
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, want := range []bool{true, false} {
		body := `{"enabled":false}`
		if want {
			body = `{"enabled":true}`
		}
		resp, err := http.Post(ts.URL+"/api/sysproxy", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST system proxy: %v", err)
		}
		var got struct {
			Supported, Enabled bool
			Proxy              string
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !got.Supported || got.Enabled != want || got.Proxy == "" {
			t.Fatalf("POST enabled=%v answered %+v", want, got)
		}
	}
}
