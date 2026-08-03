package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestReadKeyRedactsCredentialBearingGETResponses(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.SetSetting("session.enabled", "1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("session.headers", "Authorization: Bearer session-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("session.hostHeaders", `{"api.example.com":"Cookie: host-secret"}`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("session.macro", `{"enabled":true,"target":"https://example.com","request":"GET /refresh HTTP/1.1\r\nAuthorization: Bearer macro-secret\r\n","extract":"csrf"}`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("session.loginMacro", `{"enabled":true,"target":"https://example.com","request":"POST /login HTTP/1.1\r\nCookie: login-secret\r\n","refreshSecs":30}`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("authz.identities", `[ {"name":"admin","headers":"Authorization: Bearer identity-secret"} ]`); err != nil {
		t.Fatal(err)
	}
	readKey, _, err := st.CreateAPIKey("viewer", store.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/session", "/api/authz"} {
		resp := credentialGET(t, h, path, "Bearer "+readKey, true)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.Code)
		}
		body := resp.Body.String()
		secrets := []string{"identity-secret"}
		if path == "/api/session" {
			secrets = []string{"session-secret", "host-secret", "macro-secret", "login-secret"}
		}
		for _, secret := range secrets {
			if contains(body, secret) {
				t.Fatalf("GET %s exposed %q: %s", path, secret, body)
			}
		}
	}
}

func TestFullKeyAndLoopbackRetainCredentialBearingGETResponses(t *testing.T) {
	h, st, _ := newHub(t)
	_ = st.SetSetting("session.headers", "Authorization: Bearer session-secret")
	_ = st.SetSetting("session.hostHeaders", `{"api.example.com":"Cookie: host-secret"}`)
	_ = st.SetSetting("session.macro", `{"enabled":true,"target":"https://example.com","request":"GET /refresh HTTP/1.1\r\nAuthorization: Bearer macro-secret\r\n"}`)
	_ = st.SetSetting("session.loginMacro", `{"enabled":true,"target":"https://example.com","request":"POST /login HTTP/1.1\r\nCookie: login-secret\r\n"}`)
	_ = st.SetSetting("authz.identities", `[{"name":"admin","headers":"Authorization: Bearer identity-secret"}]`)
	fullKey, _, err := st.CreateAPIKey("agent", store.ScopeFull, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		auth   string
		remote bool
	}{
		{name: "full key", auth: "Bearer " + fullKey, remote: true},
		{name: "loopback trust", remote: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/api/session", "/api/authz"} {
				resp := credentialGET(t, h, path, tc.auth, tc.remote)
				if resp.Code != http.StatusOK {
					t.Fatalf("GET %s: status %d", path, resp.Code)
				}
				body := resp.Body.String()
				secrets := []string{"identity-secret"}
				if path == "/api/session" {
					secrets = []string{"session-secret", "host-secret", "macro-secret", "login-secret"}
				}
				for _, secret := range secrets {
					if !contains(body, secret) {
						t.Fatalf("GET %s omitted %q: %s", path, secret, body)
					}
				}
			}
		})
	}
}

func credentialGET(t *testing.T, h *Hub, path, auth string, remote bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://control.example"+path, nil)
	if remote {
		r.Host = "control.example"
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("10.0.0.9"), Port: 9966}))
	} else {
		r.Host = "127.0.0.1:9966"
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, r)
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("GET %s returned invalid JSON: %v", path, err)
	}
	return rec
}

func contains(text, want string) bool {
	return strings.Contains(text, want)
}
