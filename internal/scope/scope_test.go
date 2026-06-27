package scope

import (
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func flow(host, path, scheme string, port int) *store.Flow {
	return &store.Flow{Host: host, Path: path, Scheme: scheme, Port: port}
}

// Wildcard scope must not be tricked by hosts that merely contain the base
// domain as a substring or non-dot-boundary suffix — the classic wildcard
// confusion (evil-acme.com, acme.com.evil.com). Also pins scheme case-folding
// and port-only matching, none of which had boundary coverage.
func TestScopeMatchingEdgeCases(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "*.acme.com"}})
	wildcard := map[string]bool{
		"acme.com":           true,  // base
		"api.acme.com":       true,  // real subdomain
		"a.b.acme.com":       true,  // deep subdomain
		"evil-acme.com":      false, // NOT a subdomain — must not match
		"acme.com.evil.com":  false, // base as a left-suffix — must not match
		"notacme.com":        false,
		"xacme.com":          false,
	}
	for host, want := range wildcard {
		if got := e.InScope(flow(host, "/", "https", 443)); got != want {
			t.Fatalf("wildcard InScope(%s) = %v, want %v", host, got, want)
		}
	}

	// Exact host rule matches only that host, not its subdomains.
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "acme.com"}})
	if e.InScope(flow("api.acme.com", "/", "https", 443)) {
		t.Fatal("exact host rule must not match a subdomain")
	}
	if !e.InScope(flow("acme.com", "/", "https", 443)) {
		t.Fatal("exact host rule must match the host itself")
	}

	// Scheme is case-insensitive; port-only rule (empty host) gates by port.
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Scheme: "HTTPS", Port: 8443}})
	if !e.InScope(flow("anything.test", "/", "https", 8443)) {
		t.Fatal("scheme should match case-insensitively and empty host should match any host")
	}
	if e.InScope(flow("anything.test", "/", "https", 443)) {
		t.Fatal("port-only rule must not match a different port")
	}
}

func TestIncludeExcludeWildcard(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{
		{Enabled: true, Action: "include", Host: "*.acme.com"},
		{Enabled: true, Action: "exclude", Host: "analytics.acme.com"},
	})
	cases := map[string]bool{
		"app.acme.com":       true,  // subdomain included
		"acme.com":           true,  // base matched by *.acme.com
		"analytics.acme.com": false, // excluded wins
		"cdn.other.com":      false, // not included
	}
	for host, want := range cases {
		if got := e.InScope(flow(host, "/", "https", 443)); got != want {
			t.Fatalf("InScope(%s) = %v, want %v", host, got, want)
		}
	}
}

func TestExcludeOnlyMeansEverythingElseInScope(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "exclude", Host: "*.doubleclick.net"}})
	if !e.InScope(flow("victim.test", "/", "https", 443)) {
		t.Fatal("with exclude-only, an unrelated host should be in scope")
	}
	if e.InScope(flow("ad.doubleclick.net", "/", "https", 443)) {
		t.Fatal("excluded host should be out of scope")
	}
}

func TestNoRulesEverythingInScope(t *testing.T) {
	if !New().InScope(flow("anything.test", "/x", "http", 80)) {
		t.Fatal("with no rules, everything is in scope")
	}
}

func TestPathSchemePort(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "api.x", Path: "/v1", Scheme: "https"}})
	if !e.InScope(flow("api.x", "/v1/users", "https", 443)) {
		t.Fatal("path prefix should match")
	}
	if e.InScope(flow("api.x", "/v2", "https", 443)) {
		t.Fatal("different path should be out")
	}
	if e.InScope(flow("api.x", "/v1", "http", 443)) {
		t.Fatal("scheme mismatch should be out")
	}
}

func TestDisabledRulesIgnored(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{{Enabled: false, Action: "include", Host: "only.this"}})
	// The only rule is disabled → no active includes → everything in scope.
	if !e.InScope(flow("other.host", "/", "https", 443)) {
		t.Fatal("disabled rules must be ignored")
	}
}

func TestHostInScopeIgnoresPath(t *testing.T) {
	e := New()
	e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "127.0.0.1", Path: "/in"}})
	if !e.HostInScope("127.0.0.1") {
		t.Fatal("host include with path constraint should still match host for session gate")
	}
	if e.HostInScope("other.test") {
		t.Fatal("unrelated host should be out of scope")
	}
}
