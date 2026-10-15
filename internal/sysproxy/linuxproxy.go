package sysproxy

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// This file holds the pure Linux (GNOME) proxy logic — the gsettings argument
// vectors, the mode parsing, and the enable/disable/restore state machine. It
// depends only on the gsettingsRunner seam, so it compiles and is unit-tested on
// every OS. The real `gsettings` exec lives in sysproxy_linux.go behind a build
// tag.

// gsettingsRunner runs one `gsettings` invocation and returns its combined
// output. It is a seam so the command construction and state machine are
// testable without GNOME installed.
type gsettingsRunner func(args ...string) ([]byte, error)

const (
	gnomeProxySchema      = "org.gnome.system.proxy"
	gnomeProxyHTTPSchema  = "org.gnome.system.proxy.http"
	gnomeProxyHTTPSSchema = "org.gnome.system.proxy.https"
)

// linuxProxyState remembers the proxy mode that was in effect before Interseptor
// first enabled its proxy, so Disable restores it instead of hard-coding "none".
// It is process-scoped: a prior mode captured in one run is not persisted across
// restarts (GNOME exposes no spare key to stash it in), which only means a
// restart-then-Disable falls back to "none" — the common case anyway.
var (
	linuxStateMu   sync.Mutex
	linuxPriorMode string
	linuxBackupSet bool
)

// resetLinuxState clears the remembered prior mode. Used by tests.
func resetLinuxState() {
	linuxStateMu.Lock()
	linuxPriorMode, linuxBackupSet = "", false
	linuxStateMu.Unlock()
}

// gnomeEnableArgs is the ordered list of `gsettings set` invocations that point
// GNOME's manual HTTP+HTTPS proxy at host:port and switch the mode to manual.
func gnomeEnableArgs(host string, port int) [][]string {
	p := strconv.Itoa(port)
	return [][]string{
		{"set", gnomeProxyHTTPSchema, "host", host},
		{"set", gnomeProxyHTTPSchema, "port", p},
		{"set", gnomeProxyHTTPSSchema, "host", host},
		{"set", gnomeProxyHTTPSSchema, "port", p},
		{"set", gnomeProxySchema, "mode", "manual"},
	}
}

// gnomeModeArgs sets the top-level proxy mode (none/manual/auto).
func gnomeModeArgs(mode string) []string {
	return []string{"set", gnomeProxySchema, "mode", mode}
}

// parseGnomeMode strips the quoting gsettings puts around string values, so
// "'manual'\n" becomes "manual".
func parseGnomeMode(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), "'\"")
}

// linuxEnable records the current mode once, then applies the manual proxy.
func linuxEnable(run gsettingsRunner, host string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid proxy port %d", port)
	}
	if err := linuxBackupModeOnce(run); err != nil {
		return err
	}
	for _, args := range gnomeEnableArgs(host, port) {
		if _, err := run(args...); err != nil {
			return fmt.Errorf("gsettings %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

func linuxBackupModeOnce(run gsettingsRunner) error {
	linuxStateMu.Lock()
	defer linuxStateMu.Unlock()
	if linuxBackupSet {
		return nil
	}
	out, err := run("get", gnomeProxySchema, "mode")
	if err != nil {
		return fmt.Errorf("gsettings get %s mode: %w", gnomeProxySchema, err)
	}
	linuxPriorMode = parseGnomeMode(string(out))
	linuxBackupSet = true
	return nil
}

// linuxDisable restores the remembered prior mode, or "none" if none was saved.
func linuxDisable(run gsettingsRunner) error {
	linuxStateMu.Lock()
	mode := "none"
	if linuxBackupSet && linuxPriorMode != "" {
		mode = linuxPriorMode
	}
	linuxPriorMode, linuxBackupSet = "", false
	linuxStateMu.Unlock()

	args := gnomeModeArgs(mode)
	if _, err := run(args...); err != nil {
		return fmt.Errorf("gsettings %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// linuxStatus reports whether the GNOME proxy mode is currently "manual".
func linuxStatus(run gsettingsRunner) (bool, error) {
	out, err := run("get", gnomeProxySchema, "mode")
	if err != nil {
		return false, err
	}
	return parseGnomeMode(string(out)) == "manual", nil
}
