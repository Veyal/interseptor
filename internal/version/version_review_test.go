package version

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The passive startup check and `interseptor update` must agree: a release
// candidate is older than the final release of the same core version.
func TestFinishLatestCheckOffersFinalReleaseToPrerelease(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	for _, c := range []struct {
		cur, latest string
		newer       bool
	}{
		{"3.0.0-rc1", "3.0.0", true},
		{"3.0.0-local", "3.0.0", false}, // a local build of the same release
		{"3.0.0+build5", "3.0.0", false},
		{"3.0.0", "3.0.0", false},
		{"3.0.1-rc1", "3.0.0", false},
		{"2.9.9", "3.0.0", true},
	} {
		Version = c.cur
		if String() != c.cur {
			t.Skipf("build info overrides the fallback version (%q)", String())
		}
		latest, newer, err := finishLatestCheck(c.latest)
		if err != nil || latest != c.latest || newer != c.newer {
			t.Errorf("running %s, latest %s: newer=%v err=%v, want newer=%v", c.cur, c.latest, newer, err, c.newer)
		}
	}
}

// A leftover <exe>.new (an interrupted earlier update) keeps its old mode when
// it is overwritten; the installed binary must be executable regardless.
func TestInstallBinaryUnixIgnoresStaleStagingFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix install path")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "interseptor")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".new", []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installBinaryUnix(dest, []byte("new binary"), ""); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %v", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(dest); string(got) != "new binary" {
		t.Fatalf("dest holds %q", got)
	}
	if _, err := os.Stat(dest + ".new"); !os.IsNotExist(err) {
		t.Fatalf("staging file left behind: %v", err)
	}
}
