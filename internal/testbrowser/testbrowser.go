// Package testbrowser launches the operator's installed Chrome/Chromium pointed
// at the Interseptor proxy, in an isolated profile — the one-click "test browser"
// from Burp. It reuses the same browser discovery as PDF export (internal/pdfexport)
// so there is a single place that knows where Chrome lives, and it is deliberately
// fire-and-forget: it starts the browser and returns, never blocking the control
// handler and never sitting on the proxy's forwarding hot path.
package testbrowser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Veyal/interseptor/internal/pdfexport"
)

// LaunchResult describes a launched test browser.
type LaunchResult struct {
	Browser    string `json:"browser"`    // resolved browser binary
	ProfileDir string `json:"profileDir"` // isolated --user-data-dir
	Proxy      string `json:"proxy"`      // proxy the browser was pointed at
	PID        int    `json:"pid"`        // process id of the launched browser
}

// Seams, overridden in tests so Launch can be exercised without a real browser.
var (
	// resolveBrowser finds a Chrome/Chromium binary exactly the way PDF export
	// does (INTERSEPTOR_CHROME override, then PATH, then the usual install dirs).
	resolveBrowser = func() (string, error) { return pdfexport.New().Browser() }
	// profileDir returns the isolated user-data-dir, created if missing.
	profileDir = defaultProfileDir
	// startDetached starts the browser without waiting for it to exit.
	startDetached = realStartDetached
)

// Available reports whether a browser could be located to launch.
func Available() bool {
	_, err := resolveBrowser()
	return err == nil
}

// Launch starts the test browser pointed at proxy (a host:port such as
// 127.0.0.1:8080) using an isolated profile. It returns an honest error when no
// browser is installed, naming the override env var and the binaries looked for.
func Launch(proxy string) (LaunchResult, error) {
	bin, err := resolveBrowser()
	if err != nil {
		return LaunchResult{}, fmt.Errorf("cannot launch test browser: %w", err)
	}
	dir, err := profileDir()
	if err != nil {
		return LaunchResult{}, fmt.Errorf("cannot create isolated browser profile: %w", err)
	}
	args := BrowserArgs(proxy, dir, "")
	pid, err := startDetached(bin, args)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("start test browser %s: %w", bin, err)
	}
	return LaunchResult{Browser: bin, ProfileDir: dir, Proxy: proxy, PID: pid}, nil
}

// BrowserArgs builds the Chromium command line for a throwaway test browser:
//   - --proxy-server points every protocol at the Interseptor proxy;
//   - --proxy-bypass-list=<-loopback> forces even localhost targets through it;
//   - --user-data-dir isolates the profile from the operator's real browser so
//     personal history, cookies and extensions never touch captured traffic;
//   - --ignore-certificate-errors lets HTTPS load through the MITM without the
//     throwaway profile having to trust the Interseptor CA (as Burp's embedded
//     browser does — the tradeoff is that this browser cannot itself flag a bad
//     origin certificate; the proxy still can, via origin TLS verification).
//
// startURL, when non-empty, is appended so the browser opens straight on a target.
func BrowserArgs(proxy, userDataDir, startURL string) []string {
	args := []string{
		"--proxy-server=" + proxy,
		"--proxy-bypass-list=<-loopback>",
		"--user-data-dir=" + userDataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--ignore-certificate-errors",
	}
	if startURL != "" {
		args = append(args, startURL)
	}
	return args
}

// defaultProfileDir is a stable per-user directory so the test browser keeps its
// session across launches during an engagement, while staying isolated from the
// operator's real browser profile.
func defaultProfileDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "interseptor", "test-browser-profile")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// realStartDetached starts the browser and returns immediately. The process is
// reaped in the background so it does not become a zombie, but Launch never waits
// on it — the browser outlives the control request on purpose.
func realStartDetached(bin string, args []string) (int, error) {
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}
