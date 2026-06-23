package version

import "testing"

func TestPickLatest(t *testing.T) {
	tags := []string{"v0.2.1", "v0.2.0", "v0.1.0", "latest", "nightly"}

	// current is the newest → no update
	if l, n := pickLatest(tags, "0.2.1"); l != "0.2.1" || n {
		t.Fatalf("up-to-date: got latest=%q newer=%v", l, n)
	}
	// current is behind → update available
	if l, n := pickLatest(tags, "0.2.0"); l != "0.2.1" || !n {
		t.Fatalf("behind: got latest=%q newer=%v", l, n)
	}
	// a higher tag exists, non-semver tags ignored
	if l, n := pickLatest([]string{"v1.0.0", "foo", "v0.9.9"}, "0.2.1"); l != "1.0.0" || !n {
		t.Fatalf("ahead: got latest=%q newer=%v", l, n)
	}
	// nothing parseable
	if l, n := pickLatest([]string{"foo", "bar"}, "0.2.1"); l != "" || n {
		t.Fatalf("no semver tags: got latest=%q newer=%v", l, n)
	}
	// patch vs minor ordering
	if l, _ := pickLatest([]string{"v0.2.9", "v0.10.0", "v0.2.10"}, "0.0.0"); l != "0.10.0" {
		t.Fatalf("ordering: expected 0.10.0, got %q", l)
	}
}

func TestParseSemver(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{{"v1.2.3", true}, {"1.2.3", true}, {"v0.2.1-rc1", true}, {"0.2", true}, {"x", false}, {"", false}} {
		if got := parseSemver(c.in) != nil; got != c.ok {
			t.Errorf("parseSemver(%q) ok=%v want %v", c.in, got, c.ok)
		}
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"v0.2.1", true}, {"0.2.1", true}, {"v0.2.1+dirty", true},
		{"(devel)", false}, {"", false},
		{"v0.2.2-0.20260623120000-abcdef123456", false}, // pseudo-version
		{"v0.2.1-rc1", false},
	} {
		if got := isReleaseVersion(c.in); got != c.ok {
			t.Errorf("isReleaseVersion(%q)=%v want %v", c.in, got, c.ok)
		}
	}
}

func TestStringFallsBackToConst(t *testing.T) {
	// In `go test` the main module version is "(devel)", so String() returns the const.
	if String() != Version {
		t.Fatalf("String()=%q, expected baked-in %q in test builds", String(), Version)
	}
}
