package control

import (
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
