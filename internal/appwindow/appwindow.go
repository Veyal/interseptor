// Package appwindow opens the control UI as a standalone app window: an
// installed Chromium-family browser (Chrome, Edge, Chromium, Brave) started in
// --app mode with its own profile, so the UI gets its own window and
// Dock/taskbar entry with no tabs, address bar or extensions. It is the same
// embedded web UI; nothing is rendered natively.
//
// Open returns an error when no such browser or no desktop session is
// available; callers log the URL for the operator to open in any browser.
package appwindow

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/pdfexport"
)

// ErrNoDisplay reports that this machine has no desktop session to show a
// window on (for example a Linux host with neither X11 nor Wayland).
var ErrNoDisplay = errors.New("no desktop session")

// launchGrace is how long Open watches the launched process for an immediate
// failure. A browser that is still running after it, or that exited cleanly
// (handing the window to an already-running instance), counts as opened.
const launchGrace = 2 * time.Second

// Launcher opens app windows. The environment, browser lookup and process
// start are fields so it is testable without a browser or a display.
type Launcher struct {
	getenv  func(string) string
	goos    string
	browser func() (string, error)
	start   func(name string, args []string) error
	prepare func(profileDir string) error
}

// New returns a Launcher that finds the browser the same way PDF export does
// (INTERSEPTOR_CHROME, then PATH, then the usual install locations).
func New() *Launcher {
	return &Launcher{
		getenv:  os.Getenv,
		goos:    runtime.GOOS,
		browser: pdfexport.New().Browser,
		start:   startWatched,
		prepare: ensurePrivateDir,
	}
}

// AppArgs are the browser flags for an app window on url backed by profileDir.
// A dedicated profile keeps the window out of the operator's normal browser
// session (no extensions, its own Dock icon) and lets --app take effect even
// while that browser is already running.
func AppArgs(url, profileDir string) []string {
	return []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
}

// ProfileDir is the browser profile the app window uses, under the user's
// cache directory. Without one it falls back to a per-user name in the temp
// directory; Open still refuses it unless it is private to this user.
func ProfileDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return filepath.Join(os.TempDir(), "Interseptor-app-window-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, "Interseptor", "app-window")
}

// ensurePrivateDir makes dir the current user's private directory. The browser
// profile holds the control UI session, so a symlink, another user's directory
// or one others can read is refused (an own directory is tightened to 0700).
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s is not a plain directory", dir)
	}
	if !ownedByCurrentUser(fi) {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// Open shows url in an app window.
func (l *Launcher) Open(url, profileDir string) error {
	if l.goos != "darwin" && l.goos != "windows" &&
		l.getenv("DISPLAY") == "" && l.getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("%w: neither DISPLAY nor WAYLAND_DISPLAY is set", ErrNoDisplay)
	}
	bin, err := l.browser()
	if err != nil {
		return err
	}
	if l.prepare != nil {
		if err := l.prepare(profileDir); err != nil {
			return fmt.Errorf("browser profile: %w", err)
		}
	}
	name, args := l.command(bin, url, profileDir)
	return l.start(name, args)
}

// command builds the process to start. On macOS a browser inside an .app
// bundle goes through `open -n -a`: LaunchServices gives the window its own
// Dock entry, and -n stops an already-running normal browser session from
// swallowing the --app arguments.
func (l *Launcher) command(bin, url, profileDir string) (string, []string) {
	args := AppArgs(url, profileDir)
	if l.goos == "darwin" {
		if i := strings.Index(bin, ".app/Contents/MacOS/"); i >= 0 {
			return "open", append([]string{"-n", "-a", bin[:i+len(".app")], "--args"}, args...)
		}
	}
	return bin, args
}

// startWatched starts the process detached from this one and reports a
// failure only if it exits with an error within launchGrace.
func startWatched(name string, args []string) error {
	cmd := exec.Command(name, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s exited: %w", filepath.Base(name), err)
		}
		return nil
	case <-time.After(launchGrace):
		return nil
	}
}
