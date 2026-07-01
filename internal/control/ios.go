package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interceptor/internal/bind"
	"github.com/Veyal/interceptor/internal/ios"
)

func (h *iosAPI) getIOSStatus(w http.ResponseWriter, r *http.Request) {
	rep := map[string]any{
		"simctlAvailable":     ios.SimctlAvailable(),
		"ideviceAvailable":    ios.IDeviceAvailable(),
		"proxy":               h.currentProxyAddr(),
		"controlAddr":         h.currentControlAddr(),
		"devices":             []ios.Device{},
		"externalBindAllowed": bind.ExternalBindAllowed(),
		"profilePath":         "/api/ios/profile.mobileconfig",
	}
	if lan, err := ios.LANHost(); err == nil {
		rep["lanHost"] = lan
	}
	devs, err := ios.AllDevices()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep["devices"] = devs
	writeJSON(w, http.StatusOK, rep)
}

func (h *iosAPI) getIOSProfile(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	port := atoiOr(r.URL.Query().Get("port"), 0)
	if host == "" || port <= 0 {
		host, port = proxyHostPort(h.currentProxyAddr())
	}
	if host == "" || port <= 0 {
		httpErr(w, http.StatusBadRequest, "proxy host/port unknown — set proxy listen address in Settings")
		return
	}
	body, err := ios.BuildMobileConfig(h.ca.CertPEM(), ios.ProfileOpts{
		DisplayName: "Interceptor",
		ProxyHost:   host,
		ProxyPort:   port,
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor.mobileconfig"`)
	w.Write(body)
}

type iosRequest struct {
	UDID      string `json:"udid"`
	Target    string `json:"target"`
	ProxyMode string `json:"proxyMode"`
	WiFiHost  string `json:"wifiHost"`
}

func (h *iosAPI) iosDeviceAndPort(in iosRequest) (ios.Device, int, error) {
	devs, err := ios.AllDevices()
	if err != nil {
		return ios.Device{}, 0, err
	}
	d, err := ios.ResolveDevice(in.UDID, devs)
	if err != nil {
		return ios.Device{}, 0, err
	}
	_, port := proxyHostPort(h.currentProxyAddr())
	return d, port, nil
}

func (h *iosAPI) iosWiFiHost(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override), nil
	}
	return ios.LANHost()
}

func (h *iosAPI) validateIOSWiFiProxy(port int) error {
	host, _ := proxyHostPort(h.currentProxyAddr())
	if isLoopbackHost(host) {
		return fmt.Errorf("Wi‑Fi proxy needs Interceptor listening on a LAN address — rebind to 0.0.0.0:%d in Settings → Proxy", port)
	}
	return nil
}

func (h *iosAPI) profileBaseURL(r *http.Request) string {
	if r != nil && r.Host != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + r.Host
	}
	return "http://" + h.currentControlAddr()
}

func (h *iosAPI) postIOSSetup(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	var in iosRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, port, err := h.iosDeviceAndPort(in)
	if err != nil {
		// No device: still return profile URL for manual physical setup.
		if in.UDID == "" {
			host := "127.0.0.1"
			mode := strings.ToLower(strings.TrimSpace(in.ProxyMode))
			if mode == "wifi" {
				if err := h.validateIOSWiFiProxy(port); err != nil {
					httpErr(w, http.StatusBadRequest, err.Error())
					return
				}
				host, err = h.iosWiFiHost(in.WiFiHost)
				if err != nil {
					httpErr(w, http.StatusBadRequest, err.Error())
					return
				}
			}
			profileURL := h.profileBaseURL(r) + "/api/ios/profile.mobileconfig?host=" + host + "&port=" + strconv.Itoa(port)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "profileUrl": profileURL, "proxy": host + ":" + strconv.Itoa(port),
				"needsUserAction": true,
				"message":         "Open the profile URL on the iPhone (Safari), install the profile, then Settings → General → About → Certificate Trust Settings → enable full trust for Interceptor CA.",
			})
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	proxyMode := strings.ToLower(strings.TrimSpace(in.ProxyMode))
	if proxyMode == "wifi" || dev.Kind == ios.KindPhysical {
		if err := h.validateIOSWiFiProxy(port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	wifiHost := strings.TrimSpace(in.WiFiHost)
	if proxyMode == "wifi" || dev.Kind == ios.KindPhysical {
		host, err := h.iosWiFiHost(wifiHost)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		wifiHost = host
	}
	res, err := ios.Setup(dev, h.ca.CertPEM(), ios.SetupOpts{
		Target:     in.Target,
		ProxyMode:  proxyMode,
		WiFiHost:   wifiHost,
		Port:       port,
		ProfileURL: h.profileBaseURL(r),
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "udid": res.UDID, "kind": res.Kind, "proxy": res.Proxy,
		"profileUrl": res.ProfileURL, "steps": res.Steps,
		"needsUserAction": res.NeedsUserAction, "message": res.Message,
	})
}

func (h *iosAPI) postIOSInstallCA(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	if !ios.SimctlAvailable() {
		httpErr(w, http.StatusBadRequest, "Xcode simctl not available — install Xcode on macOS for simulator CA automation")
		return
	}
	var in iosRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, _, err := h.iosDeviceAndPort(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dev.Kind != ios.KindSimulator {
		httpErr(w, http.StatusBadRequest, "automated CA install works on iOS Simulator only — use the configuration profile on a physical iPhone")
		return
	}
	if err := ios.InstallCASimulator(dev, h.ca.CertPEM()); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "udid": dev.UDID,
		"message": "Simulator CA installed — enable full trust in Settings → General → About → Certificate Trust Settings if HTTPS still fails",
	})
}

func (h *iosAPI) postIOSOpenProfile(w http.ResponseWriter, r *http.Request) {
	var in iosRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, port, err := h.iosDeviceAndPort(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ios.SimctlAvailable() {
		httpErr(w, http.StatusBadRequest, "simctl not available")
		return
	}
	host := "127.0.0.1"
	if strings.ToLower(in.ProxyMode) == "wifi" {
		host, err = h.iosWiFiHost(in.WiFiHost)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	url := h.profileBaseURL(r) + "/api/ios/profile.mobileconfig?host=" + host + "&port=" + strconv.Itoa(port)
	if err := ios.OpenURL(dev, url); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "profileUrl": url,
		"message": "Opened profile install URL in simulator Safari — tap Allow and install the profile",
	})
}
