package control

import (
	"net"
	"net/http"
	"strconv"
)

// getTestBrowser reports whether a test browser can be launched and the proxy it
// would be pointed at, so the UI can show or hide the "Launch test browser" action.
func (h *settingsAPI) getTestBrowser(w http.ResponseWriter, r *http.Request) {
	available := h.testBrowserAvailable
	if available == nil {
		available = func() bool { return false }
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": available(),
		"proxy":     h.testBrowserProxy(),
	})
}

// postTestBrowserLaunch starts an isolated Chrome/Chromium pointed at the proxy.
// It is fire-and-forget and never touches the proxy's forwarding path.
func (h *settingsAPI) postTestBrowserLaunch(w http.ResponseWriter, r *http.Request) {
	launch := h.launchTestBrowser
	if launch == nil {
		httpErr(w, http.StatusServiceUnavailable, "test browser launcher is unavailable")
		return
	}
	res, err := launch(h.testBrowserProxy())
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "browser": res.Browser, "profileDir": res.ProfileDir,
		"proxy": res.Proxy, "pid": res.PID,
		"message": "Test browser launched in an isolated profile pointed at " + res.Proxy,
	})
}

// testBrowserProxy resolves the loopback host:port a browser on this machine
// should use for the proxy (a 0.0.0.0/:: bind becomes 127.0.0.1).
func (h *settingsAPI) testBrowserProxy() string {
	host, port := proxyHostPort(h.currentProxyAddr())
	return net.JoinHostPort(host, strconv.Itoa(port))
}
