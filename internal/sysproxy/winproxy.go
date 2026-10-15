package sysproxy

import (
	"fmt"
	"net"
	"strconv"
)

// This file holds the pure Windows proxy logic — the WinINet registry value
// names, the ProxyServer string shape, and the enable/disable/restore state
// machine. It depends only on the winRegistry seam, never on a Windows API, so
// it compiles and is unit-tested on every OS. The real registry access lives in
// sysproxy_windows.go behind a build tag.

// WinINet reads these values under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings.
const (
	winProxyEnableName = "ProxyEnable" // DWORD: 1 on, 0 off
	winProxyServerName = "ProxyServer" // "host:port" applies to every protocol

	// Interseptor stashes the operator's prior proxy under its own value names so
	// Disable restores exactly what was there (e.g. a corporate proxy) instead of
	// blindly turning the proxy off. The marker distinguishes "no backup" from "a
	// backup whose saved ProxyServer was the empty string".
	winBackupServerName = "InterseptorProxyServerBackup"
	winBackupEnableName = "InterseptorProxyEnableBackup"
	winBackupMarkerName = "InterseptorProxyBackupPresent"
)

// winRegistry is the seam over the HKCU Internet Settings key and the WinINet
// refresh. getString/getDWord report ok=false when the value is absent.
type winRegistry interface {
	getString(name string) (val string, ok bool, err error)
	getDWord(name string) (val uint32, ok bool, err error)
	setString(name, val string) error
	setDWord(name string, val uint32) error
	deleteValue(name string) error
	refresh() error // InternetSetOption so running apps pick up the change
}

// winProxyServer builds the ProxyServer registry string for a MITM proxy. A
// bare "host:port" (no scheme prefixes) tells WinINet to use it for HTTP and
// HTTPS alike, which is what an intercepting proxy wants.
func winProxyServer(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// winEnable points WinINet at host:port, saving the prior ProxyServer/ProxyEnable
// once so Disable can restore them. Calling it twice does not overwrite the saved
// originals with Interseptor's own values.
func winEnable(reg winRegistry, host string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid proxy port %d", port)
	}
	if err := winBackupOnce(reg); err != nil {
		return err
	}
	if err := reg.setString(winProxyServerName, winProxyServer(host, port)); err != nil {
		return err
	}
	if err := reg.setDWord(winProxyEnableName, 1); err != nil {
		return err
	}
	return reg.refresh()
}

// winBackupOnce records the current ProxyServer/ProxyEnable under the backup
// value names, but only if no backup is already present.
func winBackupOnce(reg winRegistry) error {
	if _, present, err := reg.getDWord(winBackupMarkerName); err != nil {
		return err
	} else if present {
		return nil
	}
	server, _, err := reg.getString(winProxyServerName)
	if err != nil {
		return err
	}
	enable, _, err := reg.getDWord(winProxyEnableName)
	if err != nil {
		return err
	}
	if err := reg.setString(winBackupServerName, server); err != nil {
		return err
	}
	if err := reg.setDWord(winBackupEnableName, enable); err != nil {
		return err
	}
	return reg.setDWord(winBackupMarkerName, 1)
}

// winDisable restores the saved prior proxy if Interseptor made a backup,
// otherwise it just turns ProxyEnable off (leaving any ProxyServer in place).
func winDisable(reg winRegistry) error {
	_, present, err := reg.getDWord(winBackupMarkerName)
	if err != nil {
		return err
	}
	if present {
		server, _, err := reg.getString(winBackupServerName)
		if err != nil {
			return err
		}
		enable, _, err := reg.getDWord(winBackupEnableName)
		if err != nil {
			return err
		}
		if err := reg.setString(winProxyServerName, server); err != nil {
			return err
		}
		if err := reg.setDWord(winProxyEnableName, enable); err != nil {
			return err
		}
		for _, name := range []string{winBackupServerName, winBackupEnableName, winBackupMarkerName} {
			if err := reg.deleteValue(name); err != nil {
				return err
			}
		}
		return reg.refresh()
	}
	if err := reg.setDWord(winProxyEnableName, 0); err != nil {
		return err
	}
	return reg.refresh()
}

// winStatus reports whether ProxyEnable is currently set.
func winStatus(reg winRegistry) (bool, error) {
	v, ok, err := reg.getDWord(winProxyEnableName)
	if err != nil {
		return false, err
	}
	return ok && v == 1, nil
}
