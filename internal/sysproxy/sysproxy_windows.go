//go:build windows

package sysproxy

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func init() { active = windowsBackend{reg: realWinRegistry{}} }

// windowsBackend drives the WinINet per-user proxy settings. All of the logic
// (value shapes, backup/restore state machine) lives in winproxy.go; this type
// only supplies the real registry + refresh seam.
type windowsBackend struct{ reg winRegistry }

func (windowsBackend) supported() bool                   { return true }
func (b windowsBackend) enable(host string, p int) error { return winEnable(b.reg, host, p) }
func (b windowsBackend) disable() error                  { return winDisable(b.reg) }
func (b windowsBackend) status() (bool, error)           { return winStatus(b.reg) }

// internetSettingsPath is the per-user WinINet key.
const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// realWinRegistry implements winRegistry against the live HKCU registry and the
// WinINet refresh notification.
type realWinRegistry struct{}

func (realWinRegistry) getString(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (realWinRegistry) getDWord(name string) (uint32, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return 0, false, err
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return uint32(v), true, nil
}

func (realWinRegistry) setString(name, val string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, val)
}

func (realWinRegistry) setDWord(name string, val uint32) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, val)
}

func (realWinRegistry) deleteValue(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// WinINet options for refresh (from wininet.h).
const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

var (
	wininetDLL            = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption = wininetDLL.NewProc("InternetSetOptionW")
)

// refresh asks WinINet to re-read the proxy settings so already-running apps
// (browsers, etc.) pick them up without a restart. It is best-effort: the
// registry write is the real change, and a failed notification only delays when
// existing processes notice it, so refresh never fails Enable/Disable.
func (realWinRegistry) refresh() error {
	_, _, _ = procInternetSetOption.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	_, _, _ = procInternetSetOption.Call(0, uintptr(internetOptionRefresh), 0, 0)
	return nil
}
