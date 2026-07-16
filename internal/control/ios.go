package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/ios"
)

func (h *iosAPI) getIOSStatus(w http.ResponseWriter, r *http.Request) {
	rep := map[string]any{
		"simctlAvailable":     ios.SimctlAvailable(),
		"ideviceAvailable":    ios.IDeviceAvailable(),
		"proxy":               h.currentProxyAddr(),
		"proxyAddrs":          h.currentProxyAddrs(),
		"controlAddr":         h.currentControlAddr(),
		"devices":             []ios.Device{},
		"externalBindAllowed": bind.ExternalBindAllowed(),
		"profilePath":         "/api/ios/profile.mobileconfig",
		"deviceProxy":         h.resolveDeviceEndpoint().Endpoint,
		"deviceProxyMode":     loadDeviceProxyMode(h.st),
	}
	if ep := h.resolveDeviceEndpoint(); ep.SuggestedLAN != "" {
		rep["lanHost"] = ep.SuggestedLAN
	}
	// Degrade gracefully: on a host with no iOS tooling (or a transient enumeration
	// error) AllDevices errors, but the status endpoint should still report
	// availability flags and endpoints with an empty device list — not fail the
	// whole page with a 400.
	if devs, err := ios.AllDevices(); err != nil {
		rep["deviceError"] = err.Error()
	} else {
		rep["devices"] = devs
	}
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
		host, port = h.deviceProxyHostPort("")
	}
	if host == "" || port <= 0 {
		httpErr(w, http.StatusBadRequest, "proxy host/port unknown — set proxy listen address in Settings")
		return
	}
	body, err := ios.BuildMobileConfig(h.ca.CertPEM(), ios.ProfileOpts{
		DisplayName: "Interseptor",
		ProxyHost:   host,
		ProxyPort:   port,
	})
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="interseptor.mobileconfig"`)
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
	_, port := h.deviceProxyHostPort("")
	return d, port, nil
}

func (h *iosAPI) iosWiFiHost(override string) (string, error) {
	host, _ := h.deviceProxyHostPort(override)
	return host, nil
}

func (h *iosAPI) validateIOSWiFiProxy(port int) error {
	if h.hasExternalProxyOnPort(port) {
		return nil
	}
	return fmt.Errorf("Wi‑Fi proxy needs Interseptor listening on a LAN address — rebind to 0.0.0.0:%d in Settings → Proxy", port)
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
		// No device: still return profile URL for manual physical setup. The
		// port from iosDeviceAndPort is 0 here — it short-circuited on the
		// device-lookup error before ever computing a proxy port — so it must
		// NOT be reused. Recompute the real configured proxy port directly;
		// it doesn't depend on a device being present.
		if in.UDID == "" {
			_, port := h.deviceProxyHostPort("")
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
				"message":         "Open the profile URL on the iPhone (Safari), install the profile, then Settings → General → About → Certificate Trust Settings → enable full trust for Interseptor CA.",
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
