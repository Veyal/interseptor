//go:build linux

package sysproxy

import (
	"os/exec"
)

func init() { active = linuxBackend{run: realGsettings} }

// linuxBackend drives the GNOME system proxy via `gsettings`. The argument
// construction and restore state machine live in linuxproxy.go; this type
// supplies the real exec seam and the availability probe.
type linuxBackend struct{ run gsettingsRunner }

// supported reports whether `gsettings` is on PATH. Non-GNOME desktops (KDE,
// headless servers) usually lack it; there Supported() is false and the UI
// tells the operator to set the proxy manually (see docs/proxy-and-tls.md).
func (linuxBackend) supported() bool {
	_, err := exec.LookPath("gsettings")
	return err == nil
}

func (b linuxBackend) enable(host string, p int) error { return linuxEnable(b.run, host, p) }
func (b linuxBackend) disable() error                  { return linuxDisable(b.run) }
func (b linuxBackend) status() (bool, error)           { return linuxStatus(b.run) }

// realGsettings runs the real `gsettings` binary.
func realGsettings(args ...string) ([]byte, error) {
	return exec.Command("gsettings", args...).CombinedOutput()
}
