package control

import (
	"net"
	"net/http"
	"strings"
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
	if mode == "manual" && host != "" {
		if _, _, err := net.SplitHostPort(host); err != nil {
			if strings.Contains(host, ":") || net.ParseIP(host) == nil {
				httpErr(w, http.StatusBadRequest, "invalid manual host")
				return
			}
		}
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
