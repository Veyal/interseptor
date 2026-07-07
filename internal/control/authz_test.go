package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func TestExtractAuthHeaders(t *testing.T) {
	h := map[string][]string{
		"Cookie":        {"session=abc"},
		"Authorization": {"Bearer tok"},
		"X-Api-Key":     {"secret"},
		"Accept":        {"*/*"},
	}
	got := extractAuthHeaders(h)
	if !strings.Contains(got, "Cookie: session=abc") {
		t.Fatalf("cookie missing: %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer tok") {
		t.Fatalf("auth missing: %q", got)
	}
	if !strings.Contains(got, "X-Api-Key: secret") {
		t.Fatalf("api key missing: %q", got)
	}
}

func TestApplyIdentityHeadersAnonymousStripsAuth(t *testing.T) {
	base := map[string][]string{
		"Cookie":        {"admin=1"},
		"Authorization": {"Bearer x"},
		"Accept":        {"*/*"},
	}
	out := applyIdentityHeaders(base, identity{Name: "anon", Headers: ""})
	if _, ok := out["Cookie"]; ok {
		t.Fatal("Cookie should be stripped for anonymous identity")
	}
	if _, ok := out["Authorization"]; ok {
		t.Fatal("Authorization should be stripped for anonymous identity")
	}
	if out["Accept"][0] != "*/*" {
		t.Fatal("non-auth headers should remain")
	}
}

func TestApplyIdentityHeadersOverridesCookie(t *testing.T) {
	base := map[string][]string{"Cookie": {"admin=1"}}
	out := applyIdentityHeaders(base, identity{Name: "user", Headers: "Cookie: user=2"})
	if out["Cookie"][0] != "user=2" {
		t.Fatalf("got %q", out["Cookie"][0])
	}
}

func TestSessionInvalid401(t *testing.T) {
	// A 401/403 accompanied by an actual auth-challenge signal (WWW-Authenticate,
	// or a redirect to a login page) is real evidence of session/credential
	// expiry.
	challenge := map[string][]string{"Www-Authenticate": {`Bearer realm="api"`}}
	if !sessionLooksInvalid(401, true, challenge) {
		t.Fatal("401 with auth + WWW-Authenticate should be invalid")
	}
	if sessionLooksInvalid(401, false, challenge) {
		t.Fatal("401 without auth is not a session error")
	}
	if sessionLooksInvalid(200, true, challenge) {
		t.Fatal("200 should be valid")
	}
	redirect := map[string][]string{"Location": {"/login?returnTo=/api/me"}}
	if !sessionLooksInvalid(302, true, redirect) {
		t.Fatal("redirect to a login page should be invalid")
	}
}

// A bare 403/401 with NO auth-challenge evidence is exactly the ambiguous case
// this fix addresses: right after a developer fixes an IDOR, the tool's own
// success (access now correctly denied) must not be reported the same way as
// "your session died" — sessionLooksInvalid must not fire without evidence.
func TestSessionInvalidRequiresEvidenceNotBareStatus(t *testing.T) {
	plainForbidden := map[string][]string{"Content-Type": {"application/json"}}
	if sessionLooksInvalid(403, true, plainForbidden) {
		t.Fatal("bare 403 with no auth-challenge signal should NOT be reported as sessionInvalid — it's ambiguous with correctly-working access control")
	}
	if sessionLooksInvalid(401, true, plainForbidden) {
		t.Fatal("bare 401 with no auth-challenge signal should NOT be reported as sessionInvalid")
	}
	// Access is still flagged as denied — the information isn't dropped, just
	// correctly labeled as ambiguous rather than "session expired".
	if !accessDenied(403, true) {
		t.Fatal("403 with auth should still be surfaced as access-denied")
	}
}

func TestAuthzSkipStatic(t *testing.T) {
	if !authzSkipStatic(&store.Flow{Path: "/app/main.js"}) {
		t.Fatal("expected .js to skip")
	}
	if authzSkipStatic(&store.Flow{Path: "/api/users"}) {
		t.Fatal("api path should not skip")
	}
}

func TestAuthzSameAccessBodyHash(t *testing.T) {
	base := authzResult{Status: 200, Length: 100, BodyHash: "abc", Mime: "application/json"}
	if !authzSameAccess(200, 100, "abc", "application/json", base) {
		t.Fatal("same hash should match")
	}
	diff := authzResult{Status: 200, Length: 100, BodyHash: "xyz", Mime: "application/json"}
	if authzSameAccess(200, 100, "abc", "application/json", diff) {
		t.Fatal("different hash should not match")
	}
}

func TestAuthzRunRequiresScopeForBulk(t *testing.T) {
	h, s, _ := newHub(t)
	_ = s.SetSetting("authz.identities", `[{"name":"admin","headers":"Cookie: a=1"}]`)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	body := `{"inScope":true}`
	resp, err := http.Post(ts.URL+"/api/authz/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bulk without include rules: status %d, want 400", resp.StatusCode)
	}
}

func TestAuthzTargetsInScope(t *testing.T) {
	h, s, _ := newHub(t)
	s.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "in.test", Enabled: true})
	h.sc.SetRules(mustListScope(s))

	in, _ := s.InsertFlow(&store.Flow{Method: "GET", Host: "in.test", Path: "/a", Scheme: "https", Port: 443, Status: 200})
	out, _ := s.InsertFlow(&store.Flow{Method: "GET", Host: "out.test", Path: "/b", Scheme: "https", Port: 443, Status: 200})
	targets := (&authzAPI{h}).authzTargets([]*store.Flow{
		mustGetFlow(s, in), mustGetFlow(s, out),
	}, true)
	if len(targets) != 1 || targets[0].ID != in {
		t.Fatalf("expected one in-scope target, got %+v", targets)
	}
}

func TestAuthzFlowAuthEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := s.InsertFlow(&store.Flow{
		Method: "GET", Host: "api.test", Path: "/me", Scheme: "https", Port: 443,
		ReqHeaders: map[string][]string{"Cookie": {"s=1"}, "Authorization": {"Bearer t"}},
		ResHeaders: map[string][]string{"Set-Cookie": {"s=1; Path=/; Expires=Wed, 09 Jun 2027 10:18:14 GMT"}},
	})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/authz/flow-auth/" + itoa(id))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["suggestedHeaders"].(string), "Cookie: s=1") {
		t.Fatalf("suggestedHeaders: %v", body["suggestedHeaders"])
	}
}

func mustListScope(s *store.Store) []store.ScopeRule {
	r, _ := s.ListScopeRules()
	return r
}

func mustGetFlow(s *store.Store, id int64) *store.Flow {
	f, err := s.GetFlow(id)
	if err != nil {
		panic(err)
	}
	return f
}
