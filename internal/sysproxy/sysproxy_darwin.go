//go:build darwin

package sysproxy

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func init() { active = darwinBackend{} }

// darwinBackend configures the macOS system proxy via `networksetup`.
type darwinBackend struct{}

func (darwinBackend) supported() bool { return true }

// enable routes the active network services through host:port (web + secure web).
func (darwinBackend) enable(host string, port int) error {
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

// disable turns the system web/secure-web proxy off on the active services.
func (darwinBackend) disable() error {
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

// status reports whether the system web proxy is currently on (best-effort: the
// first active service).
func (darwinBackend) status() (bool, error) {
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

// networkSetup is the runner for networksetup commands. It is a package-level
// variable so tests can inject a stub without changing external behaviour.
var networkSetup = func(args ...string) ([]byte, error) {
	return exec.Command("networksetup", args...).CombinedOutput()
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
