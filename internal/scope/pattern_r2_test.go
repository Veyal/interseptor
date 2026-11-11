package scope

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestClassifyPathPattern(t *testing.T) {
	for _, tc := range []struct {
		pattern  string
		kind     PathPatternKind
		wantWarn bool
	}{
		{"", PathPatternAny, false},
		{"/admin", PathPatternPrefix, false},
		{"/api/v1/users", PathPatternPrefix, false},
		{"/admin/", PathPatternRegex, true},    // slash-wrapped: unanchored regex
		{"/search?q=", PathPatternRegex, true}, // "?" makes it a regex
		{"/a+b/items", PathPatternRegex, true}, // "+" makes it a regex
		{"^/admin/", PathPatternRegex, false},  // deliberate regex
		{"/api/v[0-9]+/", PathPatternRegex, false},
		{`/users/\d+`, PathPatternRegex, false},
		{"/static/.*", PathPatternRegex, false},
	} {
		kind, warn := ClassifyPathPattern(tc.pattern)
		if kind != tc.kind || (warn != "") != tc.wantWarn {
			t.Errorf("ClassifyPathPattern(%q) = %q, warning %q; want %q, warning=%v", tc.pattern, kind, warn, tc.kind, tc.wantWarn)
		}
	}
}

// The classification must describe what the engine really does, and the
// suggested anchored form must mean "paths under this directory".
func TestClassifyPathPatternAgreesWithMatching(t *testing.T) {
	match := func(pattern, path string) bool {
		e := New()
		e.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "example.com", Path: pattern}})
		return e.InScope(&store.Flow{Scheme: "https", Host: "example.com", Port: 443, Path: path})
	}
	kind, warn := ClassifyPathPattern("/admin/")
	if kind != PathPatternRegex || !match("/admin/", "/x/admin/y") {
		t.Fatalf("/admin/ kind=%q, matches /x/admin/y=%v: classification and matching disagree", kind, match("/admin/", "/x/admin/y"))
	}
	if !strings.Contains(warn, `"^/admin/"`) {
		t.Fatalf("warning does not name the anchored form: %q", warn)
	}
	if !match("^/admin/", "/admin/users") || match("^/admin/", "/x/admin/y") {
		t.Fatal("the suggested anchored form does not behave as a directory prefix")
	}
	if kind, _ := ClassifyPathPattern("/admin"); kind != PathPatternPrefix || match("/admin", "/x/admin") || !match("/admin", "/admin/users") {
		t.Fatal("/admin must be a literal prefix")
	}
}
