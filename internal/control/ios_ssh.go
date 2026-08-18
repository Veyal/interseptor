package control

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/ios"
)

const maxMobileCommandRequestBytes int64 = 64 << 10

type iosSSHRequest struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	KeyPath   string `json:"keyPath"`
	ProxyHost string `json:"proxyHost"`
	WiFiHost  string `json:"wifiHost"`
}

func (in iosSSHRequest) sshOpts() ios.SSHOpts {
	return ios.SSHOpts{
		Host:     in.Host,
		Port:     in.Port,
		User:     in.User,
		Password: in.Password,
		KeyPath:  in.KeyPath,
	}
}

func (h *iosAPI) iosSSHMeta() map[string]any {
	rep := map[string]any{
		"sshAvailable":        ios.SSHAvailable(),
		"proxy":               h.currentProxyAddr(),
		"proxyAddrs":          h.currentProxyAddrs(),
		"controlAddr":         h.currentControlAddr(),
		"externalBindAllowed": bind.ExternalBindAllowed(),
		"profilePath":         "/api/ios/profile.mobileconfig",
		"defaultUser":         "root",
		"defaultPort":         22,
		"deviceProxy":         h.resolveDeviceEndpoint().Endpoint,
		"deviceProxyMode":     loadDeviceProxyMode(h.st),
	}
	if ep := h.resolveDeviceEndpoint(); ep.SuggestedLAN != "" {
		rep["lanHost"] = ep.SuggestedLAN
	}
	return rep
}

func (h *iosAPI) getIOSSSHStatus(w http.ResponseWriter, r *http.Request) {
	rep := h.iosSSHMeta()
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host != "" {
		port := 22
		if rawPort := r.URL.Query().Get("port"); rawPort != "" {
			parsed, err := strconv.Atoi(rawPort)
			if err != nil || parsed < 1 || parsed > 65535 {
				httpErr(w, http.StatusBadRequest, "port must be between 1 and 65535")
				return
			}
			port = parsed
		}
		rep["host"] = host
		rep["port"] = port
		rep["reachable"] = h.iosTCPReachable(host, port)
		if !rep["reachable"].(bool) {
			rep["message"] = "TCP port not reachable — ensure OpenSSH is running and the device is on the same network"
		} else {
			rep["message"] = "TCP port open — POST credentials to /api/ios/ssh/status for full SSH authentication check"
		}
	}
	writeJSON(w, http.StatusOK, rep)
}

func (h *iosAPI) postIOSSSHStatus(w http.ResponseWriter, r *http.Request) {
	var in iosSSHRequest
	if !decodeOptionalLimitedJSON(w, r, maxMobileCommandRequestBytes, &in) {
		return
	}
	res, err := ios.SSHStatus(in.sshOpts())
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              res.Authenticated,
		"host":            res.Host,
		"user":            res.User,
		"port":            res.Port,
		"reachable":       res.Reachable,
		"authenticated":   res.Authenticated,
		"steps":           res.Steps,
		"message":         res.Message,
		"needsUserAction": false,
	})
}

func (h *iosAPI) postIOSSSHInstallCA(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	var in iosSSHRequest
	if !decodeOptionalLimitedJSON(w, r, maxMobileCommandRequestBytes, &in) {
		return
	}
	_, port := h.deviceProxyHostPort("")
	if err := h.validateIOSWiFiProxy(port); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	host, _ := h.deviceProxyHostPort(firstNonEmpty(in.ProxyHost, in.WiFiHost))
	profileURL := ios.BuildProfileURL(h.profileBaseURL(r), host, port)
	res, err := ios.SSHInstallCA(in.sshOpts(), profileURL)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "host": res.Host, "user": res.User, "method": res.Method,
		"profileUrl": res.ProfileURL, "proxy": host + ":" + itoa64(int64(port)),
		"steps": res.Steps, "needsUserAction": res.NeedsUserAction, "message": res.Message,
	})
}

func (h *iosAPI) postIOSSSHSetup(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	var in iosSSHRequest
	if !decodeOptionalLimitedJSON(w, r, maxMobileCommandRequestBytes, &in) {
		return
	}
	_, port := h.deviceProxyHostPort("")
	if err := h.validateIOSWiFiProxy(port); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	host, _ := h.deviceProxyHostPort(firstNonEmpty(in.ProxyHost, in.WiFiHost))
	res, err := ios.SSHSetup(ios.SSHSetupOpts{
		SSHOpts:    in.sshOpts(),
		ProxyHost:  host,
		ProxyPort:  port,
		ProfileURL: h.profileBaseURL(r),
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "host": res.Host, "user": res.User, "method": res.Method,
		"profileUrl": res.ProfileURL, "proxy": res.Proxy,
		"steps": res.Steps, "needsUserAction": res.NeedsUserAction,
		"message": res.Message, "warning": res.Warning,
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
