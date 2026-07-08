package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/android"
	"github.com/Veyal/interseptor/internal/bind"
)

func (h *androidAPI) getAndroidStatus(w http.ResponseWriter, r *http.Request) {
	rep := map[string]any{
		"available":           android.Available(),
		"proxy":               h.currentProxyAddr(),
		"proxyAddrs":          h.currentProxyAddrs(),
		"devices":             []android.Device{},
		"externalBindAllowed": bind.ExternalBindAllowed(),
		"deviceProxy":         h.resolveDeviceEndpoint().Endpoint,
		"deviceProxyMode":     loadDeviceProxyMode(h.st),
	}
	if ep := h.resolveDeviceEndpoint(); ep.SuggestedLAN != "" {
		rep["lanHost"] = ep.SuggestedLAN
	}
	if !android.Available() {
		writeJSON(w, http.StatusOK, rep)
		return
	}
	devs, err := android.Devices()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep["devices"] = devs
	serial := r.URL.Query().Get("serial")
	d, err := android.ResolveDevice(serial, devs)
	if err == nil {
		if pv, err := android.ProxyValue(d); err == nil {
			rep["proxyValue"] = pv
			rep["proxyActive"] = pv != ""
			rep["proxySerial"] = d.Serial
		}
	}
	writeJSON(w, http.StatusOK, rep)
}

type androidRequest struct {
	Serial         string `json:"serial"`
	Mode           string `json:"mode"`
	ProxyMode      string `json:"proxyMode"`
	CAMode         string `json:"caMode"`
	WiFiHost       string `json:"wifiHost"`
	RemoveSystemCA bool   `json:"removeSystemCA"`
}

func (h *androidAPI) androidDeviceAndPort(in androidRequest) (android.Device, int, error) {
	devs, err := android.Devices()
	if err != nil {
		return android.Device{}, 0, err
	}
	d, err := android.ResolveDevice(in.Serial, devs)
	if err != nil {
		return android.Device{}, 0, err
	}
	_, port := h.deviceProxyHostPort("")
	return d, port, nil
}

func (h *androidAPI) androidWiFiHost(override string) (string, error) {
	host, _ := h.deviceProxyHostPort(override)
	return host, nil
}

func (h *androidAPI) validateWiFiProxy(port int) error {
	if h.hasExternalProxyOnPort(port) {
		return nil
	}
	return fmt.Errorf("wifi proxy needs Interseptor listening on a LAN address — rebind to 0.0.0.0:%d in Settings → Proxy", port)
}

func (h *androidAPI) postAndroidProxy(w http.ResponseWriter, r *http.Request) {
	if !android.Available() {
		httpErr(w, http.StatusBadRequest, "adb not found on PATH — install Android platform-tools")
		return
	}
	var in androidRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, port, err := h.androidDeviceAndPort(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(in.ProxyMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(in.Mode))
	}
	if mode == "" {
		mode = "usb"
	}
	var proxyAddr string
	switch mode {
	case "wifi":
		if err := h.validateWiFiProxy(port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		host, err := h.androidWiFiHost(in.WiFiHost)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := android.EnableProxyWiFi(dev, host, port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		proxyAddr = host + ":" + itoa64(int64(port))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "serial": dev.Serial, "proxy": proxyAddr, "proxyMode": "wifi",
			"message": "Device global proxy set to " + proxyAddr + " — ensure the device is on the same network",
		})
	default:
		if err := android.EnableProxyUSB(dev, port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		proxyAddr = "127.0.0.1:" + itoa64(int64(port))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "serial": dev.Serial, "proxy": proxyAddr, "proxyMode": "usb",
			"message": "USB reverse enabled and device global proxy set — browse on the device to capture traffic",
		})
	}
}

func (h *androidAPI) postAndroidUnproxy(w http.ResponseWriter, r *http.Request) {
	if !android.Available() {
		httpErr(w, http.StatusBadRequest, "adb not found on PATH — install Android platform-tools")
		return
	}
	var in androidRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, port, err := h.androidDeviceAndPort(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var cert []byte
	if in.RemoveSystemCA {
		if h.ca == nil {
			httpErr(w, http.StatusNotFound, "no CA")
			return
		}
		cert = h.ca.CertPEM()
	}
	res, err := android.Teardown(dev, port, cert, in.RemoveSystemCA)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "serial": res.Serial, "steps": res.Steps,
		"message": res.Message, "warning": res.Warning,
	})
}

func (h *androidAPI) postAndroidInstallCA(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	if !android.Available() {
		httpErr(w, http.StatusBadRequest, "adb not found on PATH — install Android platform-tools")
		return
	}
	var in androidRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	mode := in.Mode
	if mode == "" {
		mode = in.CAMode
	}
	if mode == "" {
		mode = "user"
	}
	devs, err := android.Devices()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dev, err := android.ResolveDevice(in.Serial, devs)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if mode == "auto" {
		mode = android.EnrichDevice(dev).SuggestedCAMode
	}
	if mode != "user" && mode != "system" {
		httpErr(w, http.StatusBadRequest, "mode must be user, system, or auto")
		return
	}
	cert := h.ca.CertPEM()
	var msg string
	switch mode {
	case "user":
		if err := android.InstallUserCA(dev, cert); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		msg = "CA pushed — confirm the install prompt on the device (name the cert if asked). Most apps on Android 7+ ignore user CAs unless the app opts in or the device is rooted."
	case "system":
		if err := android.InstallSystemCA(dev, cert); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		msg = "System CA installed — reboot the device so all apps pick up the new trust store"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "serial": dev.Serial, "mode": mode,
		"needsUserAction": mode == "user", "message": msg,
	})
}

func (h *androidAPI) postAndroidSetup(w http.ResponseWriter, r *http.Request) {
	if h.ca == nil {
		httpErr(w, http.StatusNotFound, "no CA")
		return
	}
	if !android.Available() {
		httpErr(w, http.StatusBadRequest, "adb not found on PATH — install Android platform-tools")
		return
	}
	var in androidRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dev, port, err := h.androidDeviceAndPort(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	proxyMode := strings.ToLower(strings.TrimSpace(in.ProxyMode))
	if proxyMode == "" {
		proxyMode = "usb"
	}
	wifiHost := strings.TrimSpace(in.WiFiHost)
	if proxyMode == "wifi" {
		if err := h.validateWiFiProxy(port); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		host, err := h.androidWiFiHost(wifiHost)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		wifiHost = host
	}
	caMode := in.CAMode
	if caMode == "" {
		caMode = "auto"
	}
	res, err := android.Setup(dev, h.ca.CertPEM(), android.SetupOpts{
		ProxyMode: proxyMode,
		CAMode:    caMode,
		WiFiHost:  wifiHost,
		Port:      port,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "serial": res.Serial, "proxy": res.Proxy, "caMode": res.CAMode,
		"steps": res.Steps, "needsUserAction": res.NeedsUserAction, "message": res.Message,
	})
}
