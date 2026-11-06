package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// cspFlow is an HTTPS HTML page whose only weakness is the CSP under test.
func cspFlow(csp string) *store.Flow {
	return &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html"},
			"Strict-Transport-Security": {"max-age=63072000"},
			"Content-Security-Policy":   {csp},
			"Referrer-Policy":           {"no-referrer"},
			"X-Frame-Options":           {"DENY"},
		}),
	}
}

// A wildcard *source* is a bare "*" token in the directive. A path wildcard
// (https://cdn.example.com/assets/*) restricts the policy to one host, so
// flagging it was a false positive on a very common CDN pattern.
func TestWeakCSPWildcardIsATokenNotAPathGlob(t *testing.T) {
	for _, tc := range []struct {
		name, csp string
		want      bool
	}{
		{"bare wildcard", "default-src *", true},
		{"bare wildcard after sources", "script-src 'self' *", true},
		{"wildcard in a second policy", "default-src 'self', script-src *", true},
		{"cdn path wildcard", "script-src 'self' https://cdn.example.com/assets/*", false},
		{"cdn path wildcard in a later directive", "default-src 'self'; script-src https://cdn.example.com/*", false},
		{"no wildcard at all", "default-src 'self'; script-src 'self' 'nonce-abc'", false},
		{"unsafe-inline is still weak", "script-src 'self' 'unsafe-inline'", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(cspFlow(tc.csp), nil, []byte("<html></html>"))
			if has(got, "Weak Content-Security-Policy") != tc.want {
				t.Fatalf("weak-csp = %v, want %v: %s", !tc.want, tc.want, titles(got))
			}
		})
	}
}
