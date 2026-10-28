package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Changing headers through PATCH invalidates the previous session check.
func TestPatchIdentityHeadersClearsLastCheck(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"user","headers":"Authorization: Bearer old-token"}]}`)
	if _, err := (&authzAPI{h}).mutateIdentities(func(cur []identity) ([]identity, error) {
		for i := range cur {
			cur[i].LastCheck = &identityCheck{TS: "2026-01-01T00:00:00Z", Status: 200, Verdict: identityVerdictOK}
		}
		return cur, nil
	}); err != nil {
		t.Fatal(err)
	}
	// A PATCH that does not touch headers keeps the check.
	code, _ := authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/user", `{"notes":"n"}`, nil)
	if code != http.StatusOK || authzIdentityNamed(t, h, "user").LastCheck == nil {
		t.Fatalf("notes patch status=%d dropped lastCheck", code)
	}
	code, _ = authzReq(t, http.MethodPatch, ts.URL+"/api/authz/identities/user", `{"headers":"Authorization: Bearer new-token"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d", code)
	}
	if got := authzIdentityNamed(t, h, "user"); got.LastCheck != nil {
		t.Fatalf("stale lastCheck kept after header change: %+v", got.LastCheck)
	}
}
