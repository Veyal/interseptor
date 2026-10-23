package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newIdentityServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func identityDetail(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()
	code, out := authzReq(t, http.MethodGet, ts.URL+"/api/authz/identities/"+url.PathEscape(name), "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET %q = %d %v", name, code, out)
	}
	return identityOf(t, out)
}

// The legacy Authz modal saves with mode "replace" and only {name, headers,
// broken}: that must not erase provenance, role, notes or health.
func TestAuthzReplaceKeepsIdentityMetadata(t *testing.T) {
	ts := newIdentityServer(t)
	ai := map[string]string{"X-Interseptor-Source": "ai"}
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", `{"name":"svc","headers":"Cookie: sid=abcdefghijkl","role":"user","notes":"made by agent"}`, ai)
	if code, _ := authzReq(t, http.MethodPost, ts.URL+"/api/authz", `{"mode":"replace","identities":[{"name":"svc","headers":"Cookie: sid=abcdefghijkl","broken":false}]}`, nil); code != http.StatusOK {
		t.Fatalf("replace save = %d", code)
	}
	id := identityDetail(t, ts, "svc")
	src, _ := id["source"].(map[string]any)
	if id["createdBy"] != "agent" || id["role"] != "user" || id["notes"] != "made by agent" || src == nil || src["kind"] != "agent" {
		t.Fatalf("replace save erased metadata: %+v", id)
	}
}

// Server-owned fields come from the server, and an AI request cannot label
// itself as the human operator.
func TestAuthzAgentCannotClaimHumanProvenance(t *testing.T) {
	ts := newIdentityServer(t)
	body := `{"name":"spoof","headers":"Cookie: sid=abcdefghijkl","owner":"you","createdBy":"you","source":{"kind":"manual"},"lastCheck":{"verdict":"ok","status":200}}`
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", body, map[string]string{"X-Interseptor-Source": "ai"})
	id := identityDetail(t, ts, "spoof")
	src, _ := id["source"].(map[string]any)
	if id["createdBy"] == "you" || id["owner"] == "you" || src == nil || src["kind"] != "agent" || id["lastCheck"] != nil {
		t.Fatalf("agent spoofed human provenance or health: %+v", id)
	}
}

func TestIdentityNamesWithPercentAreAddressable(t *testing.T) {
	ts := newIdentityServer(t)
	authzReq(t, http.MethodPost, ts.URL+"/api/authz/identity", `{"name":"100% admin","headers":"Cookie: sid=abcdefghijkl"}`, nil)
	identityDetail(t, ts, "100% admin")
	if code, _ := authzReq(t, http.MethodDelete, ts.URL+"/api/authz/identity/"+url.PathEscape("100% admin"), "", nil); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("delete %%-name = %d", code)
	}
}

func TestMaskSecretHidesShortValues(t *testing.T) {
	for _, v := range []string{"abcdefgh", "abcdefghijklmno"} {
		if got := maskSecret(v); got != "••••" {
			t.Errorf("maskSecret(%q) = %q, want fully hidden", v, got)
		}
	}
	if got := maskSecret("Bearer abcdefghijklmnop"); got != "Bear…op" {
		t.Errorf("long value mask = %q", got)
	}
}
