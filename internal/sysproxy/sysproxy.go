// Package sysproxy points the operating system's HTTP/HTTPS proxy at Interseptor
// (and turns it back off). It only ever acts on an explicit user request — it is
// never enabled automatically. macOS is supported via `networksetup`; on other
// platforms callers should set the proxy (127.0.0.1:8080) manually.
package sysproxy

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// networkSetup is the runner for networksetup commands. It is a package-level
// variable so tests can inject a stub without changing external behaviour.
var networkSetup = func(args ...string) ([]byte, error) {
	return exec.Command("networksetup", args...).CombinedOutput()
}

// Supported reports whether automatic configuration is available on this OS.
func Supported() bool { return runtime.GOOS == "darwin" }

// Enable routes the active network services through host:port (web + secure web).
func Enable(host string, port int) error {
	if !Supported() {
		return fmt.Errorf("automatic system-proxy config is only supported on macOS; set your OS proxy to %s:%d manually", host, port)
	}
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	for _, s := range svcs {
		for _, args := range [][]string{
			{"-setwebproxy", s, host, p},
			{"-setsecurewebproxy", s, host, p},
			{"-setwebproxystate", s, "on"},
			{"-setsecurewebproxystate", s, "on"},
		} {
			if err := run(args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// Disable turns the system web/secure-web proxy off on the active services.
func Disable() error {
	if !Supported() {
		return nil
	}
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	for _, s := range svcs {
		_ = run("-setwebproxystate", s, "off")
		_ = run("-setsecurewebproxystate", s, "off")
	}
	return nil
}

// Status reports whether the system web proxy is currently on (best-effort: the
// first active service).
func Status() (bool, error) {
	if !Supported() {
		return false, nil
	}
	svcs, err := activeServices()
	if err != nil || len(svcs) == 0 {
		return false, err
	}
	out, err := networkSetup("-getwebproxy", svcs[0])
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "Enabled: Yes"), nil
}

func run(args ...string) error {
	out, err := networkSetup(args...)
	if err != nil {
		return fmt.Errorf("networksetup %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func activeServices() ([]string, error) {
	out, err := networkSetup("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	return parseServices(string(out)), nil
}

// parseServices extracts the enabled network services from
// `networksetup -listallnetworkservices` output (skipping the header and any
// service prefixed with "*", which marks it disabled).
func parseServices(output string) []string {
	var svcs []string
	for i, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 || strings.TrimSpace(line) == "" { // first line is a header
			continue
		}
		if strings.HasPrefix(line, "*") { // disabled
			continue
		}
		svcs = append(svcs, line)
	}
	return svcs
}
