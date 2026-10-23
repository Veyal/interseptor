package control

import (
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/proc"
	"github.com/Veyal/interseptor/internal/sysproxy"
)

const maxSystemProxyRequestBytes int64 = 4 << 10

func (h *settingsAPI) getSysProxy(w http.ResponseWriter, r *http.Request) {
	status := h.sysProxyStatus
	if status == nil {
		status = sysproxy.Status
	}
	supported := h.sysProxySupported
	if supported == nil {
		supported = sysproxy.Supported
	}
	enabled, _ := status()
	writeJSON(w, http.StatusOK, map[string]any{
		"supported": supported(),
		"enabled":   enabled,
		"proxy":     h.currentProxyAddr(),
	})
}

func (h *settingsAPI) setSysProxy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxSystemProxyRequestBytes, &in) {
		return
	}
	if in.Enabled {
		host, port := proxyHostPort(h.currentProxyAddr())
		enable := h.sysProxyEnable
		if enable == nil {
			enable = sysproxy.Enable
		}
		if err := enable(host, port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		h.setSysProxyMarker(net.JoinHostPort(host, strconv.Itoa(port)))
	} else {
		if err := h.disableSysProxy(); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		h.clearSysProxyMarker()
	}
	h.getSysProxy(w, r)
}

// sysProxyMarkerName is a file in the global data dir recording that the OS
// proxy was switched on by Interseptor ("<pid> <addr>"), so it can be switched
// back off on graceful shutdown or, after a crash / kill, on the next start.
// Projects share the global dir, so the pid says which instance owns it.
const sysProxyMarkerName = "sysproxy.enabled"

func (h *Hub) sysProxyMarkerPath() string {
	if h.GlobalDir == "" {
		return ""
	}
	return filepath.Join(h.GlobalDir, sysProxyMarkerName)
}

func (h *Hub) setSysProxyMarker(addr string) {
	if p := h.sysProxyMarkerPath(); p != "" {
		if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+" "+addr+"\n"), 0o600); err != nil {
			log.Printf("system proxy: persist enabled marker: %v", err)
		}
	}
}

func (h *Hub) clearSysProxyMarker() {
	if p := h.sysProxyMarkerPath(); p != "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("system proxy: clear enabled marker: %v", err)
		}
	}
}

// sysProxyMarker reports whether a marker exists, the pid that wrote it (0 when
// unreadable or from an older release) and the proxy address it recorded.
func (h *Hub) sysProxyMarker() (ok bool, pid int, addr string) {
	p := h.sysProxyMarkerPath()
	if p == "" {
		return false, 0, ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, 0, ""
	}
	fields := strings.Fields(string(b))
	if len(fields) == 2 {
		pid, _ = strconv.Atoi(fields[0])
		addr = fields[1]
	} else if len(fields) == 1 {
		addr = fields[0]
	}
	return true, pid, addr
}

func (h *Hub) sysProxyOwnerRunning(pid int) bool {
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	alive := h.sysProxyOwnerAlive
	if alive == nil {
		alive = proc.AliveInterseptor
	}
	return alive(pid)
}

func (h *Hub) disableSysProxy() error {
	disable := h.sysProxyDisable
	if disable == nil {
		disable = sysproxy.Disable
	}
	return disable()
}

// releaseMarkedSysProxy turns the OS proxy off and clears the marker. A failed
// disable keeps the marker so the next start retries.
func (h *Hub) releaseMarkedSysProxy() error {
	if err := h.disableSysProxy(); err != nil {
		return err
	}
	h.clearSysProxyMarker()
	return nil
}

// ReleaseSystemProxy turns the OS proxy back off on graceful shutdown, but
// only when this instance enabled it: another running instance's setting (the
// marker names its pid) is left alone.
func (h *Hub) ReleaseSystemProxy() error {
	ok, pid, _ := h.sysProxyMarker()
	if !ok || (pid != 0 && pid != os.Getpid()) {
		return nil
	}
	return h.releaseMarkedSysProxy()
}

// RecoverSystemProxy runs at startup: a marker whose owner is no longer running
// means the OS proxy still points at a dead listener, so turn it off. A
// project-switch re-exec takes over the running setting instead (adopting the
// marker, since a re-spawn has a new pid), and a marker owned by another live
// instance is left alone.
func (h *Hub) RecoverSystemProxy(projectSwitch bool) error {
	ok, pid, addr := h.sysProxyMarker()
	if !ok {
		return nil
	}
	if projectSwitch {
		h.setSysProxyMarker(addr)
		return nil
	}
	if h.sysProxyOwnerRunning(pid) {
		return nil
	}
	return h.releaseMarkedSysProxy()
}

// proxyHostPort resolves the address a *client* should use for the proxy: a
// 0.0.0.0/:: bind becomes loopback.
func proxyHostPort(addr string) (string, int) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 8080
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return host, atoiOr(p, 8080)
}
