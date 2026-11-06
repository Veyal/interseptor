package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A present-but-zero max-age is the strongest possible HSTS weakness: it tells
// the browser to forget the policy immediately, which is exactly the SSL-strip
// window the check exists to flag. The parser used to return 0 both for "no
// max-age" and for "max-age=0", so the worst value was silently skipped.
func TestWeakHSTSFlagsMaxAgeZero(t *testing.T) {
	flow := func(hsts string) *store.Flow {
		return &store.Flow{
			Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200,
			Mime: "application/json",
			ResHeaders: map[string][]string(http.Header{
				"Content-Type":              {"application/json"},
				"Strict-Transport-Security": {hsts},
			}),
		}
	}
	for _, tc := range []struct {
		name, hsts string
		want       bool
	}{
		{"max-age=0 disables HSTS", "max-age=0", true},
		{"max-age=0 with includeSubDomains", "max-age=0; includeSubDomains", true},
		{"one second under 180 days", "max-age=15551999", true},
		{"exactly 180 days", "max-age=15552000", false},
		{"no max-age directive", "includeSubDomains", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(flow(tc.hsts), nil, []byte(`{"ok":true}`))
			if has(got, "Weak HSTS policy (short max-age)") != tc.want {
				t.Fatalf("weak-hsts = %v, want %v: %s", !tc.want, tc.want, titles(got))
			}
		})
	}
}

// The shipped Starlark override flags max-age=0 (its digit heuristic matches a
// single digit), so the Go check must agree or saving the template would change
// the result.
func TestWeakHSTSMaxAgeZeroMatchesTheTemplate(t *testing.T) {
	f := &store.Flow{
		ID: 1, Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200,
		Mime: "application/json",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=0"},
		}),
	}
	star := hasTitle(runTemplate(t, checkWeakHSTS, f, "", ""), "Weak HSTS policy (short max-age)")
	goSide := hasTitle(onlyCheck(f, nil, nil, checkWeakHSTS), "Weak HSTS policy (short max-age)")
	if !star || !goSide {
		t.Fatalf("max-age=0: template found = %v, Go found = %v, want both true", star, goSide)
	}
}
