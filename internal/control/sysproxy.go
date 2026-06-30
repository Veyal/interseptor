package control

import (
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/Veyal/interceptor/internal/sysproxy"
)

func (h *settingsAPI) getSysProxy(w http.ResponseWriter, r *http.Request) {
	enabled, _ := sysproxy.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"supported": sysproxy.Supported(),
		"enabled":   enabled,
		"proxy":     h.currentProxyAddr(),
	})
}

func (h *settingsAPI) setSysProxy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Enabled {
		host, port := proxyHostPort(h.currentProxyAddr())
		if err := sysproxy.Enable(host, port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if err := sysproxy.Disable(); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
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
