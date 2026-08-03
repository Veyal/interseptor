package main

import (
	"errors"
	"strings"
	"testing"
)

func TestInsideAppBundle(t *testing.T) {
	tests := []struct {
		name     string
		exe      string
		wantRoot string
		wantOK   bool
	}{
		{
			name:     "standard bundle layout",
			exe:      "/Applications/Interseptor.app/Contents/MacOS/interseptor",
			wantRoot: "/Applications/Interseptor.app",
			wantOK:   true,
		},
		{
			name:     "bundle name containing spaces",
			exe:      "/Users/x/Downloads/My Proxy.app/Contents/MacOS/interseptor",
			wantRoot: "/Users/x/Downloads/My Proxy.app",
			wantOK:   true,
		},
		{
			name:     "extension case is ignored on a case-insensitive volume",
			exe:      "/Applications/Interseptor.APP/Contents/MacOS/interseptor",
			wantRoot: "/Applications/Interseptor.APP",
			wantOK:   true,
		},
		{
			name:   "plain unix install path",
			exe:    "/usr/local/bin/interseptor",
			wantOK: false,
		},
		{
			name:   "homebrew cellar path",
			exe:    "/opt/homebrew/Cellar/interseptor/1.7.2/bin/interseptor",
			wantOK: false,
		},
		{
			name:   "Contents/MacOS without a .app ancestor",
			exe:    "/srv/pkg/Contents/MacOS/interseptor",
			wantOK: false,
		},
		{
			name:   "inside a bundle but not in MacOS",
			exe:    "/Applications/Interseptor.app/Contents/Resources/interseptor",
			wantOK: false,
		},
		{
			name:   "missing the Contents level",
			exe:    "/Applications/Interseptor.app/MacOS/interseptor",
			wantOK: false,
		},
		{
			name:   "a directory merely named like a bundle",
			exe:    "/tmp/notanapp/Contents/MacOS/interseptor",
			wantOK: false,
		},
		{
			name:   "empty path",
			exe:    "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, ok := insideAppBundle(tt.exe)
			if ok != tt.wantOK {
				t.Fatalf("insideAppBundle(%q) ok = %v, want %v", tt.exe, ok, tt.wantOK)
			}
			if ok && root != tt.wantRoot {
				t.Errorf("root = %q, want %q", root, tt.wantRoot)
			}
		})
	}
}

func TestCheckSelfUpdateAllowedBlocksBundledDarwin(t *testing.T) {
	err := checkSelfUpdateAllowed("darwin", "/Applications/Interseptor.app/Contents/MacOS/interseptor", false)
	if err == nil {
		t.Fatal("self-update must be refused inside a macOS .app bundle")
	}
	// The message has to tell the user what to do instead, not just say no.
	for _, want := range []string{"Interseptor.app", "signature"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %s", want, err)
		}
	}
}

func TestCheckSelfUpdateAllowedPermitsNormalInstalls(t *testing.T) {
	for _, exe := range []string{
		"/usr/local/bin/interseptor",
		"/opt/homebrew/bin/interseptor",
		"/home/user/go/bin/interseptor",
	} {
		if err := checkSelfUpdateAllowed("darwin", exe, false); err != nil {
			t.Errorf("checkSelfUpdateAllowed(%q) = %v, want nil", exe, err)
		}
	}
}

// Only macOS has .app bundles; the same path shape on another OS is a normal
// directory and must not block updating.
func TestCheckSelfUpdateAllowedIgnoresNonDarwin(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if err := checkSelfUpdateAllowed(goos, "/srv/Interseptor.app/Contents/MacOS/interseptor", false); err != nil {
			t.Errorf("goos=%s: got %v, want nil", goos, err)
		}
	}
}

// Regression: os.Executable does NOT resolve symlinks on darwin, so invoking the
// bundled binary through a symlink (e.g. /usr/local/bin/interseptor pointing into
// /Applications/Interseptor.app) previously slipped past the bundle check and
// overwrote the binary inside the bundle, breaking its signature.
func TestGuardSelfUpdateResolvesSymlinks(t *testing.T) {
	const real = "/Applications/Interseptor.app/Contents/MacOS/interseptor"
	link := "/usr/local/bin/interseptor"
	resolve := func(p string) (string, error) {
		if p == link {
			return real, nil
		}
		return p, nil
	}

	err := guardSelfUpdate("darwin", link, false, resolve)
	if err == nil {
		t.Fatal("a symlink pointing into a bundle must still be refused")
	}
	if !strings.Contains(err.Error(), "Interseptor.app") {
		t.Errorf("error should name the resolved bundle, got: %s", err)
	}
}

func TestGuardSelfUpdateAllowsGenuineNonBundlePaths(t *testing.T) {
	resolve := func(p string) (string, error) { return p, nil }
	for _, exe := range []string{"/usr/local/bin/interseptor", "/opt/homebrew/bin/interseptor"} {
		if err := guardSelfUpdate("darwin", exe, false, resolve); err != nil {
			t.Errorf("guardSelfUpdate(%q) = %v, want nil", exe, err)
		}
	}
}

// A direct (non-symlinked) bundle path must still be caught even when symlink
// resolution fails outright.
func TestGuardSelfUpdateStillCatchesDirectPathWhenResolveFails(t *testing.T) {
	resolve := func(string) (string, error) { return "", errors.New("boom") }
	err := guardSelfUpdate("darwin", "/Applications/Interseptor.app/Contents/MacOS/interseptor", false, resolve)
	if err == nil {
		t.Fatal("direct bundle path must be refused regardless of resolver failure")
	}
}

func TestGuardSelfUpdateHonorsOverride(t *testing.T) {
	resolve := func(string) (string, error) {
		return "/Applications/Interseptor.app/Contents/MacOS/interseptor", nil
	}
	if err := guardSelfUpdate("darwin", "/usr/local/bin/interseptor", true, resolve); err != nil {
		t.Errorf("override should permit the update, got %v", err)
	}
}

func TestCheckSelfUpdateAllowedHonorsOverride(t *testing.T) {
	err := checkSelfUpdateAllowed("darwin", "/Applications/Interseptor.app/Contents/MacOS/interseptor", true)
	if err != nil {
		t.Errorf("explicit override should permit the update, got %v", err)
	}
}
