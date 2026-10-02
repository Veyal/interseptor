package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyBasicAuthSetting_uiOffersOptionalCredentials(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, want := range []string{
		`id="proxyAuthToggle"`,
		`aria-pressed="false"`,
		`id="proxyAuthUser"`,
		`id="proxyAuthPassword"`,
		`type="password"`,
		"API keys still protect History",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("settings HTML missing %q", want)
		}
	}
	settings := readUIAsset(t, "js/settings.js")
	if !strings.Contains(settings, "saveSettingsPatch({proxyAuthEnabled:enabled,proxyAuthUser:user,proxyAuthPassword:password})") {
		t.Fatal("proxy authentication save does not send the optional basic-auth fields")
	}
	if !strings.Contains(settings, "'proxyAuthUser','proxyAuthPassword'") {
		t.Fatal("proxy authentication fields are not guarded as unsaved drafts")
	}
}

func TestProxyBasicAuthSetting_defaultsOff(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s["proxyAuthEnabled"] != false || s["proxyAuthUser"] != "" || s["proxyAuthPassword"] != "" {
		t.Fatalf("default proxy auth = %#v", map[string]any{
			"enabled": s["proxyAuthEnabled"], "user": s["proxyAuthUser"], "password": s["proxyAuthPassword"],
		})
	}
}

func TestProxyBasicAuthSetting_persistsThenApplies(t *testing.T) {
	h, st, _ := newHub(t)
	var applied []bool
	var gotUser, gotPass string
	h.SetProxyBasicAuth = func(enabled bool, user, password string) {
		applied = append(applied, enabled)
		gotUser, gotPass = user, password
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(body any) *http.Response {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := put(map[string]any{"proxyAuthEnabled": true, "proxyAuthUser": " proxy ", "proxyAuthPassword": ""})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty password status = %d, want 400", resp.StatusCode)
	}
	if len(applied) != 0 {
		t.Fatal("rejected proxy auth changed runtime state")
	}

	resp = put(map[string]any{"proxyAuthEnabled": true, "proxyAuthUser": " proxy ", "proxyAuthPassword": "secret"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d, want 200", resp.StatusCode)
	}
	if len(applied) != 1 || !applied[0] || gotUser != "proxy" || gotPass != "secret" {
		t.Fatalf("applied = %v user=%q pass=%q", applied, gotUser, gotPass)
	}
	for _, key := range []string{"proxy.authEnabled", "proxy.authUser", "proxy.authPassword"} {
		if _, ok, err := st.GetSetting(key); err != nil || !ok {
			t.Fatalf("setting %s missing: ok=%v err=%v", key, ok, err)
		}
	}

	resp = put(map[string]any{"proxyAuthEnabled": false})
	var saved map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if saved["proxyAuthEnabled"] != false || saved["proxyAuthUser"] != "proxy" || saved["proxyAuthPassword"] != "secret" {
		t.Fatalf("disabled settings = %#v", saved)
	}
	if len(applied) != 2 || applied[1] {
		t.Fatalf("disable did not apply: %v", applied)
	}
}

func TestProxyBasicAuthSetting_persistenceFailureLeavesRuntimeUntouched(t *testing.T) {
	h, st, _ := newHub(t)
	called := false
	h.SetProxyBasicAuth = func(bool, string, string) { called = true }
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"proxyAuthEnabled": true, "proxyAuthUser": "proxy", "proxyAuthPassword": "secret"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if called {
		t.Fatal("runtime proxy auth changed after persistence failure")
	}
}
