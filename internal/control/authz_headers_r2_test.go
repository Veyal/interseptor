package control

import (
	"strings"
	"testing"
)

// headerValuesFold returns every value stored under any spelling of name.
func headerValuesFold(h map[string][]string, name string) []string {
	var out []string
	for k, vs := range h {
		if strings.EqualFold(k, name) {
			out = append(out, vs...)
		}
	}
	return out
}

// An identity header replaces the captured header of the same name whatever
// the spelling on either side. HTTP/2 and imported captures store names
// lower-case; adding the identity's canonical key beside them sent both the
// captured value and the identity's.
func TestApplyIdentityHeadersReplacesCaseInsensitively(t *testing.T) {
	cases := []struct {
		name     string
		base     map[string][]string
		identity string
		header   string
		want     string
	}{
		{"lower-case captured tenant", map[string][]string{"x-tenant": {"1"}}, "X-Tenant: 2", "X-Tenant", "2"},
		{"lower-case identity tenant", map[string][]string{"X-Tenant": {"1"}}, "x-tenant: 2", "X-Tenant", "2"},
		{"mixed-case captured, several values", map[string][]string{"X-TENANT": {"1", "9"}, "x-Tenant": {"7"}}, "X-Tenant: 2", "X-Tenant", "2"},
		{"lower-case captured cookie", map[string][]string{"cookie": {"admin=1"}}, "Cookie: user=2", "Cookie", "user=2"},
		{"lower-case identity authorization", map[string][]string{"Authorization": {"Bearer ADMIN"}}, "authorization: Bearer LOW", "Authorization", "Bearer LOW"},
		{"lower-case api key both sides", map[string][]string{"x-api-key": {"ADMIN"}}, "x-api-key: LOW", "X-Api-Key", "LOW"},
		{"lower-case captured user-agent", map[string][]string{"user-agent": {"captured"}}, "User-Agent: identity", "User-Agent", "identity"},
	}
	for _, tc := range cases {
		out := applyIdentityHeaders(tc.base, identity{Name: "id", Headers: tc.identity})
		got := headerValuesFold(out, tc.header)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: %s values = %q (headers %v), want only %q", tc.name, tc.header, got, out, tc.want)
		}
	}
}

// The captured request map is never modified by building a replay.
func TestApplyIdentityHeadersLeavesCapturedMapAlone(t *testing.T) {
	base := map[string][]string{"x-tenant": {"1"}, "cookie": {"admin=1"}}
	applyIdentityHeaders(base, identity{Name: "id", Headers: "X-Tenant: 2"})
	if len(base) != 2 || base["x-tenant"][0] != "1" || base["cookie"][0] != "admin=1" {
		t.Fatalf("captured headers were modified: %v", base)
	}
}
