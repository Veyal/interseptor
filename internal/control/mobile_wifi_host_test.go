package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Veyal/interseptor/internal/ios"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// A per-request wifiHost override is checked before it reaches adb or a
// profile URL, so both mobile setups answer 400 instead of building a command
// or URL from shell/URL metacharacters.
func TestMobileWiFiHostOverrideValidated(t *testing.T) {
	h, _, _ := newHub(t)
	a := &androidAPI{h}
	i := &iosAPI{h}
	for _, bad := range []string{"1.2.3.4;reboot", "h&port=1", "x$(id)"} {
		if _, err := a.androidWiFiHost(bad); err == nil {
			t.Fatalf("android accepted %q", bad)
		}
		if _, err := i.iosWiFiHost(bad); err == nil {
			t.Fatalf("ios accepted %q", bad)
		}
	}
	if host, err := i.iosWiFiHost("192.0.2.10:8081"); err != nil || host != "192.0.2.10" {
		t.Fatalf("ios host = %q, %v", host, err)
	}
}

// Profile URLs carry the proxy host as a query value, so it is escaped.
func TestBuildProfileURLEscapesHost(t *testing.T) {
	got := ios.BuildProfileURL("http://192.0.2.1:9966", "fd00::1", 8080)
	u, err := url.Parse(got)
	if err != nil || u.Query().Get("host") != "fd00::1" || u.Query().Get("port") != "8080" {
		t.Fatalf("profile url = %q", got)
	}
}

// An out-of-range port in the profile query is a client error, not a 500.
func TestIOSProfileRejectsBadPort(t *testing.T) {
	h, _, _ := newHub(t)
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.ca = ca
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/ios/profile.mobileconfig?host=192.0.2.10&port=70000")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/api/ios/profile.mobileconfig?host=" + url.QueryEscape("a\"><x") + "&port=8080")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad host status = %d, want 400", resp.StatusCode)
	}
}
