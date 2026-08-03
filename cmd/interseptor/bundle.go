package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// allowBundleUpdateEnv force-enables self-update inside a .app bundle. It exists
// for unsigned local builds, where overwriting the binary is harmless; on a
// signed bundle it will break the signature and Gatekeeper will refuse to launch
// the app afterwards.
const allowBundleUpdateEnv = "INTERSEPTOR_ALLOW_BUNDLE_UPDATE"

// insideAppBundle reports whether exePath is the main executable of a macOS
// application bundle, i.e. <name>.app/Contents/MacOS/<exe>, and returns the
// bundle root. The extension comparison is case-insensitive because macOS
// volumes usually are.
func insideAppBundle(exePath string) (string, bool) {
	if exePath == "" {
		return "", false
	}
	macOS := filepath.Dir(exePath)
	if filepath.Base(macOS) != "MacOS" {
		return "", false
	}
	contents := filepath.Dir(macOS)
	if filepath.Base(contents) != "Contents" {
		return "", false
	}
	root := filepath.Dir(contents)
	if !strings.HasSuffix(strings.ToLower(filepath.Base(root)), ".app") {
		return "", false
	}
	return root, true
}

// guardSelfUpdate refuses an in-place self-update for a bundled binary, checking
// both the invoked path and its symlink-resolved target.
//
// Both are needed: os.Executable does not resolve symlinks on darwin, so a link
// such as /usr/local/bin/interseptor pointing into
// /Applications/Interseptor.app/Contents/MacOS/ looks like an ordinary install
// until it is resolved — and following that link would overwrite the binary
// inside the bundle, breaking its signature. If resolution fails we still honor
// the verdict from the raw path rather than failing open.
func guardSelfUpdate(goos, exePath string, override bool, resolve func(string) (string, error)) error {
	if err := checkSelfUpdateAllowed(goos, exePath, override); err != nil {
		return err
	}
	resolved, err := resolve(exePath)
	if err != nil || resolved == exePath {
		return nil
	}
	return checkSelfUpdateAllowed(goos, resolved, override)
}

// checkSelfUpdateAllowed refuses an in-place self-update when running as the
// main executable of a macOS .app bundle.
//
// `interseptor update` overwrites its own executable. Inside a bundle that
// invalidates the code signature, so a signed and notarized app stops launching
// — and it would also leave the bundle's launcher and Info.plist at the old
// version. Replacing the whole bundle is the only coherent update for that
// install shape.
func checkSelfUpdateAllowed(goos, exePath string, override bool) error {
	if goos != "darwin" || override {
		return nil
	}
	root, ok := insideAppBundle(exePath)
	if !ok {
		return nil
	}
	return fmt.Errorf(
		"refusing to self-update inside the macOS app bundle at %s\n"+
			"Overwriting the binary in place would break the bundle's code signature and leave\n"+
			"its launcher and Info.plist on the old version. Replace the whole bundle instead:\n"+
			"  • download the latest Interseptor.app (or run `make macos-app`) and drag it over the old one\n"+
			"  • or install the standalone binary with Homebrew and update that copy\n"+
			"Set %s=1 to override (safe only for an unsigned local build).",
		root, allowBundleUpdateEnv)
}
