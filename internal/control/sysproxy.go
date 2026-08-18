package control

import (
	"net"
	"net/http"

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
	} else {
		disable := h.sysProxyDisable
		if disable == nil {
			disable = sysproxy.Disable
		}
		if err := disable(); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	h.getSysProxy(w, r)
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
