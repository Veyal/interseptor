package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const settingsCanary = "ExampleSecret123"

// Regression: GET /api/settings (and the MCP get_settings / check_readiness /
// ca_info tools built on it) returned the proxy-auth password and upstream
// proxy credentials in cleartext to the AI channel and API-key callers.
func TestSettingsMaskSecretsOutsideUISession(t *testing.T) {
	f := newCollFixture(t)
	f.must("PUT", "/api/settings", map[string]any{
		"proxyAuthEnabled": true, "proxyAuthUser": "tester", "proxyAuthPassword": settingsCanary,
		"upstreamProxy": "http://chain:" + settingsCanary + "@proxy.example.com:3128",
	}, asUI, 200, nil)

	for name, h := range map[string]hdrs{
		"ai source":       asAI,
		"no ui session":   {},
		"stale ui cookie": {"Cookie": uiSessionCookie + "=stale"},
	} {
		code, out := f.do("GET", "/api/settings", nil, h)
		if code != http.StatusOK {
			t.Fatalf("%s: GET settings = %d", name, code)
		}
		if strings.Contains(out, settingsCanary) {
			t.Errorf("%s: settings leaked a stored secret: %s", name, out)
		}
		var s settingsJSON
		_ = json.Unmarshal([]byte(out), &s)
		if !s.ProxyAuthPasswordSet || s.ProxyAuthUser != "tester" || !strings.Contains(s.UpstreamProxy, "proxy.example.com:3128") {
			t.Errorf("%s: masked settings lost non-secret fields: %+v", name, s)
		}
	}

	var s settingsJSON
	f.must("GET", "/api/settings", nil, asUI, 200, &s)
	if s.ProxyAuthPassword != settingsCanary || !strings.Contains(s.UpstreamProxy, settingsCanary) {
		t.Fatalf("the operator's UI session must still see its own settings: %+v", s)
	}

	// An agent that echoes the masked upstream URL back must not wipe the password.
	_, masked := f.do("GET", "/api/settings", nil, asAI)
	var ms settingsJSON
	_ = json.Unmarshal([]byte(masked), &ms)
	f.must("PUT", "/api/settings", map[string]any{"upstreamProxy": ms.UpstreamProxy}, asAI, 200, nil)
	f.must("GET", "/api/settings", nil, asUI, 200, &s)
	if !strings.Contains(s.UpstreamProxy, settingsCanary) {
		t.Fatalf("echoing the masked upstream URL replaced the stored credentials: %q", s.UpstreamProxy)
	}
}

func TestRedactProxyURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                   "",
		"http://proxy.example.com:8080":      "http://proxy.example.com:8080",
		"socks5://user@proxy.example.com:1":  "socks5://user@proxy.example.com:1",
		"http://u:p@proxy.example.com:3128":  "http://u:xxxxx@proxy.example.com:3128",
		"https://u:p%40ss@proxy.example.com": "https://u:xxxxx@proxy.example.com",
	} {
		if got := redactProxyURL(in); got != want {
			t.Errorf("redactProxyURL(%q) = %q, want %q", in, got, want)
		}
	}
}
