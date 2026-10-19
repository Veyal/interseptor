package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getSettingsMap(t *testing.T, h *Hub) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	(&settingsAPI{h}).getSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var s map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// QA: GET /api/settings returned the proxy passwords in plaintext, and MCP
// get_settings / ca_info / check_readiness pass that body to the agent. The
// secrets are write-only: the response says whether one is saved.
func TestSettingsResponseNeverCarriesSavedPasswords(t *testing.T) {
	h, st, _ := newHub(t)
	for k, v := range map[string]string{
		"upstream.proxy":     "http://alice:s3cr%3At@proxy.example.com:3128",
		proxyAuthEnabledKey:  "1",
		proxyAuthUserKey:     "qa",
		proxyAuthPasswordKey: "pw123",
	} {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	s := getSettingsMap(t, h)
	raw, _ := json.Marshal(s)
	for _, secret := range []string{"s3cr", "pw123"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("settings response leaks %q: %s", secret, raw)
		}
	}
	if s["upstreamProxy"] != "http://alice@proxy.example.com:3128" || s["upstreamProxyPasswordSet"] != true {
		t.Fatalf("upstream = %v set=%v", s["upstreamProxy"], s["upstreamProxyPasswordSet"])
	}
	if s["proxyAuthPassword"] != "" || s["proxyAuthPasswordSet"] != true || s["proxyAuthUser"] != "qa" {
		t.Fatalf("proxy auth = %v / %v / %v", s["proxyAuthUser"], s["proxyAuthPassword"], s["proxyAuthPasswordSet"])
	}
}

// The write-only path: re-saving the masked upstream URL keeps the stored
// password when asked to; a new password or a different user replaces it.
func TestUpstreamProxyKeepPasswordIsWriteOnly(t *testing.T) {
	h, st, _ := newHub(t)
	var applied []string
	h.Upstream = func(v string) error { applied = append(applied, v); return nil }
	if err := st.SetSetting("upstream.proxy", "http://alice:old@proxy.example.com:3128"); err != nil {
		t.Fatal(err)
	}
	stored := func() string { v, _, _ := st.GetSetting("upstream.proxy"); return v }
	put := func(body string) {
		t.Helper()
		if rec := putSettingsBody(t, h, body); rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", body, rec.Code, rec.Body)
		}
	}
	put(`{"upstreamProxy":"http://alice@proxy.example.com:8080","upstreamProxyKeepPassword":true}`)
	if got := stored(); got != "http://alice:old@proxy.example.com:8080" {
		t.Fatalf("kept password: stored %q", got)
	}
	if applied[len(applied)-1] != "http://alice:old@proxy.example.com:8080" {
		t.Fatalf("runtime got %q", applied[len(applied)-1])
	}
	put(`{"upstreamProxy":"http://alice:new@proxy.example.com:8080","upstreamProxyKeepPassword":true}`)
	if got := stored(); got != "http://alice:new@proxy.example.com:8080" {
		t.Fatalf("new password: stored %q", got)
	}
	// QA round 2: editing only the username with the password left blank
	// ("Saved — leave blank to keep") silently dropped the saved password.
	put(`{"upstreamProxy":"http://bob@proxy.example.com:8080","upstreamProxyKeepPassword":true}`)
	if got := stored(); got != "http://bob:new@proxy.example.com:8080" {
		t.Fatalf("a renamed user on the same proxy keeps the password: stored %q", got)
	}
	// The saved password is never sent to a different proxy host.
	put(`{"upstreamProxy":"http://bob@other.example.net:8080","upstreamProxyKeepPassword":true}`)
	if got := stored(); got != "http://bob@other.example.net:8080" {
		t.Fatalf("a different proxy host must not inherit the password: stored %q", got)
	}
	put(`{"upstreamProxy":"http://bob:new@proxy.example.com:8080"}`)
	put(`{"upstreamProxy":"socks5://bob@proxy.example.com:1080","upstreamProxyKeepPassword":true}`)
	if got := stored(); got != "socks5://bob:new@proxy.example.com:1080" {
		t.Fatalf("a scheme or port change on the same host keeps the password: stored %q", got)
	}
	put(`{"upstreamProxy":"http://alice:x@proxy.example.com:8080"}`)
	put(`{"upstreamProxy":"http://alice@proxy.example.com:8080"}`)
	if got := stored(); got != "http://alice@proxy.example.com:8080" {
		t.Fatalf("without keep, the URL is stored as sent: %q", got)
	}
}

// Leaving the proxy-auth password out of a PUT keeps the saved one.
func TestProxyAuthPasswordOmittedKeepsSavedSecret(t *testing.T) {
	h, st, _ := newHub(t)
	h.SetProxyBasicAuth = func(bool, string, string) {}
	if rec := putSettingsBody(t, h, `{"proxyAuthEnabled":true,"proxyAuthUser":"qa","proxyAuthPassword":"pw123"}`); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if rec := putSettingsBody(t, h, `{"proxyAuthEnabled":true,"proxyAuthUser":"qa2"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if v, _, _ := st.GetSetting(proxyAuthPasswordKey); v != "pw123" {
		t.Fatalf("password = %q, want kept", v)
	}
}

// The Settings UI must not resend a masked secret: a blank password field
// keeps the saved one (proxy auth omits it, upstream asks the server to keep it).
func TestUISettingsSavedPasswordsAreWriteOnly(t *testing.T) {
	js := readUIAsset(t, "js/settings.js")
	for _, want := range []string{
		"proxyAuthPasswordSaved=!!s.proxyAuthPasswordSet;",
		"parseUpstreamProxyURL(s.upstreamProxy||'',!!s.upstreamProxyPasswordSet);",
		"upstreamProxyKeepPassword:true",
		"el.placeholder=saved?'Saved — leave blank to keep':''",
		// A keep the server could not honour (new proxy host, cleared username)
		// says so; settings-model.js upstreamPasswordDropWarning has the wording.
		"const dropped=upstreamPasswordDropWarning({keep,url:upstreamProxy,passwordSaved:upstreamPasswordSaved});",
		"if(dropped)toast(dropped,'warn');",
		// A restored backup's missing passwords are shown, not hidden.
		"$('#proxyAuthPasswordMissing')?.classList.toggle('u-hidden',!s.proxyAuthPasswordMissing);",
		"$('#upstreamPasswordMissing')?.classList.toggle('u-hidden',!s.upstreamProxyPasswordMissing);",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("settings.js lacks %q", want)
		}
	}
	html := readUIAsset(t, "index.html")
	for _, id := range []string{`id="proxyAuthPasswordMissing"`, `id="upstreamPasswordMissing"`} {
		if !strings.Contains(html, id) || !strings.Contains(html, "Password not included in the backup.") {
			t.Fatalf("index.html lacks the %s notice", id)
		}
	}
	if strings.Contains(js, "s.proxyAuthPassword||''") {
		t.Fatal("settings.js must not fill the password field from the settings response")
	}
}
