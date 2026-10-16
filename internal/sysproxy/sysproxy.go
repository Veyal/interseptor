// Package sysproxy points the operating system's HTTP/HTTPS proxy at Interseptor
// (and turns it back off). It only ever acts on an explicit user request — it is
// never enabled automatically.
//
// Each operating system has its own backend, selected at init() by a
// build-tagged file: macOS via `networksetup` (sysproxy_darwin.go), Windows via
// the WinINet registry values under HKCU (sysproxy_windows.go), and Linux via
// GNOME `gsettings org.gnome.system.proxy` (sysproxy_linux.go). Any other OS —
// or a Linux desktop without gsettings — falls back to the unsupported backend,
// which reports Supported()==false and tells the caller to set 127.0.0.1:8080
// manually.
//
// The side-effecting layer (exec, registry, gsettings) is isolated behind small
// seams so the command/registry construction and the enable/disable/restore
// state machine are unit-testable on any OS without touching the real system.
package sysproxy

import (
	"fmt"
	"runtime"
)

// backend is the per-OS implementation. Exactly one is installed into active by
// a build-tagged init(); platforms with no automation keep the unsupported one.
type backend interface {
	supported() bool
	enable(host string, port int) error
	disable() error
	status() (bool, error)
}

// active is the platform backend. The default is the unsupported fallback; the
// per-OS file for darwin/windows/linux replaces it in init().
var active backend = unsupportedBackend{}

// Supported reports whether automatic configuration is available on this OS.
func Supported() bool { return active.supported() }

// Enable routes the OS HTTP/HTTPS proxy through host:port.
func Enable(host string, port int) error { return active.enable(host, port) }

// Disable turns the OS proxy back off (restoring the prior setting where the
// backend tracked one).
func Disable() error { return active.disable() }

// Status reports whether the OS proxy is currently on (best-effort).
func Status() (bool, error) { return active.status() }

// unsupportedBackend is used on any OS without proxy automation. Enable fails
// with a manual-configuration hint; Disable and Status are no-ops so callers can
// invoke them unconditionally.
type unsupportedBackend struct{}

func (unsupportedBackend) supported() bool { return false }

func (unsupportedBackend) enable(host string, port int) error {
	return fmt.Errorf("automatic system-proxy config is not supported on %s; set your OS proxy to %s:%d manually", runtime.GOOS, host, port)
}

func (unsupportedBackend) disable() error { return nil }

func (unsupportedBackend) status() (bool, error) { return false, nil }
