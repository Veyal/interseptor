package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The manual device-proxy host ends up in `adb shell settings put global
// http_proxy` and in iOS profile URLs, so only a plain IP or hostname with an
// optional numeric port is stored.
func TestDeviceEndpointManualHostValidation(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSettings(map[string]string{settingDeviceProxyMode: "auto", settingDeviceProxyHost: ""}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	post := func(host string) int {
		resp, err := http.Post(ts.URL+"/api/proxy/device-endpoint", "application/json",
			strings.NewReader(`{"mode":"manual","host":"`+host+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, bad := range []string{"a;reboot:8080", "x$(id):8080", "h`id`", "a b:80", "192.0.2.10:0", "192.0.2.10:99999", "192.0.2.10:http", "h&&id"} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Fatalf("host %q status = %d, want 400", bad, code)
		}
	}
	assertDeviceProxySettings(t, st, "auto", "")
	for _, ok := range []string{"192.0.2.10", "192.0.2.10:8081", "proxy.example.com", "proxy.example.com:8080", "[fd00::1]:8080"} {
		if code := post(ok); code != http.StatusOK {
			t.Fatalf("host %q status = %d, want 200", ok, code)
		}
	}
}
