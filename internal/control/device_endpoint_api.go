package control

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/android"
)

const maxDeviceEndpointRequestBytes int64 = 16 << 10

func (h *settingsAPI) getDeviceProxyEndpoint(w http.ResponseWriter, r *http.Request) {
	ep := h.resolveDeviceEndpoint()
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         ep.Mode,
		"host":         ep.Host,
		"port":         ep.Port,
		"endpoint":     ep.Endpoint,
		"manualHost":   loadDeviceProxyHost(h.st),
		"suggestedLAN": ep.SuggestedLAN,
		"source":       ep.Source,
		"listeners":    h.currentProxyAddrs(),
	})
}

func (h *settingsAPI) setDeviceProxyEndpoint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
		Host string `json:"host"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxDeviceEndpointRequestBytes, &in) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "manual" {
		httpErr(w, http.StatusBadRequest, "mode must be auto or manual")
		return
	}
	host := strings.TrimSpace(in.Host)
	if mode == "manual" && host != "" && !validManualDeviceHost(host) {
		httpErr(w, http.StatusBadRequest, "invalid manual host — use an IP address or hostname, optionally with :port")
		return
	}
	if err := h.st.SetSettings(map[string]string{
		settingDeviceProxyMode: mode,
		settingDeviceProxyHost: host,
	}); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.getDeviceProxyEndpoint(w, r)
}

// validManualDeviceHost accepts host or host:port where host is an IP address
// or DNS hostname and port is 1-65535. The host reaches `adb shell` and iOS
// profile URLs, so shell or URL metacharacters are refused.
func validManualDeviceHost(raw string) bool {
	host := raw
	if h, p, err := net.SplitHostPort(raw); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
		host = h
	} else if strings.Contains(raw, ":") && net.ParseIP(raw) == nil {
		return false
	}
	return android.ValidProxyHost(host)
}
