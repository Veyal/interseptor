package main

import (
	"net/url"
	"os/exec"
	"runtime"
)

// defaultBrowserCommand returns the command that opens rawURL in the user's
// default browser on goos. Only http(s) URLs qualify.
func defaultBrowserCommand(goos, rawURL string) (string, []string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", nil, false
	}
	switch goos {
	case "darwin":
		return "open", []string{rawURL}, true
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{rawURL}, true
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}, true
	}
	return "", nil, false
}

// openDefaultBrowser opens rawURL in the default browser without waiting.
func openDefaultBrowser(rawURL string) error {
	name, args, ok := defaultBrowserCommand(runtime.GOOS, rawURL)
	if !ok {
		return exec.ErrNotFound
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
