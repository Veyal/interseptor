package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func putSettingsBody(t *testing.T, h *Hub, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	(&settingsAPI{h}).putSettings(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	return rec
}

// A PUT that fails validation on a later field must not leave earlier fields
// persisted or applied (previously each field was committed one at a time).
func TestPutSettingsRejectedRequestLeavesNoPartialUpdate(t *testing.T) {
	cases := map[string]string{
		"bad proxy address":    `{"oobEnabled":true,"findingsUIEditing":true,"proxyAddrs":["not-an-address"]}`,
		"proxy auth no secret": `{"oobEnabled":true,"findingsUIEditing":true,"proxyAuthEnabled":true}`,
		"external control":     `{"oobEnabled":true,"findingsUIEditing":true,"controlAddr":"203.0.113.10:9966"}`,
		"invalid upstream CA":  `{"oobEnabled":true,"findingsUIEditing":true,"upstreamProxyCA":"not a pem"}`,
		"invalid upstream":     `{"oobEnabled":true,"findingsUIEditing":true,"upstreamProxy":"ftp://example.com:21"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", "0")
			h, st, _ := newHub(t)
			applied := false
			h.SetCaptureScopeOnly = func(bool) { applied = true }
			body = strings.Replace(body, `{`, `{"captureScopeOnly":true,`, 1)
			rec := putSettingsBody(t, h, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			for _, key := range []string{"oob.enabled", "findings.uiEditing", "capture.scopeOnly"} {
				if v, ok, _ := st.GetSetting(key); ok {
					t.Fatalf("%s persisted as %q by a rejected request", key, v)
				}
			}
			if applied {
				t.Fatal("runtime setter applied by a rejected request")
			}
		})
	}
}

func TestPutSettingsPersistsAllFieldsInOneBatch(t *testing.T) {
	h, st, _ := newHub(t)
	var batches []map[string]string
	h.setSettings = func(values map[string]string) error {
		batches = append(batches, values)
		return st.SetSettings(values)
	}
	rec := putSettingsBody(t, h, `{"oobEnabled":true,"findingsUIEditing":true,"invisibleProxy":true,"tlsBypassHosts":["example.com"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(batches) != 1 {
		t.Fatalf("persisted in %d batches, want 1", len(batches))
	}
	want := map[string]string{"oob.enabled": "1", "findings.uiEditing": "1", "proxy.invisibleProxy": "1", tlsBypassSettingKey: "example.com"}
	for k, v := range want {
		if batches[0][k] != v {
			t.Fatalf("batch[%s] = %q, want %q (batch=%v)", k, batches[0][k], v, batches[0])
		}
	}
}

func TestPutSettingsBroadcastsOnce(t *testing.T) {
	h, _, _ := newHub(t)
	ch := make(chan string, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	rec := putSettingsBody(t, h, `{"oobEnabled":true,"findingsUIEditing":true,"invisibleProxy":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	n := 0
	for len(ch) > 0 {
		if strings.Contains(<-ch, `"settings.update"`) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("settings.update broadcasts = %d, want 1", n)
	}
}

func TestPutSettingsPersistenceFailureRollsBackRuntime(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting("upstream.proxy", "http://example.com:3128"); err != nil {
		t.Fatal(err)
	}
	var upstream []string
	h.Upstream = func(v string) error { upstream = append(upstream, v); return nil }
	setterCalled := false
	h.SetInvisibleProxy = func(bool) { setterCalled = true }
	h.setSettings = func(map[string]string) error { return errors.New("store unavailable") }
	rec := putSettingsBody(t, h, `{"invisibleProxy":true,"upstreamProxy":"http://example.com:8081"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if fmt.Sprint(upstream) != "[http://example.com:8081 http://example.com:3128]" {
		t.Fatalf("upstream applications = %v, want new then previous", upstream)
	}
	if setterCalled {
		t.Fatal("runtime setter applied after persistence failure")
	}
}
