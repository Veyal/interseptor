package control

import (
	"net/http"
	"runtime"
	"sync"

	"github.com/Veyal/interseptor/internal/chromemgr"
	"github.com/Veyal/interseptor/internal/pdfexport"
)

var (
	newChromeInstaller = chromemgr.NewInstaller
	chromeInstallMu    sync.Mutex
)

// getChromeStatus reports whether Chrome/Chromium is installed and available,
// including managed Chrome in ~/.interseptor/chrome.
func (h *settingsAPI) getChromeStatus(w http.ResponseWriter, r *http.Request) {
	managedDir := chromemgr.Dir("")
	managedPath, _ := chromemgr.BinaryPath(managedDir, runtime.GOOS, runtime.GOARCH)

	browserPath, err := pdfexport.New().Browser()
	available := err == nil && browserPath != ""

	platform, platformErr := chromemgr.PlatformKey(runtime.GOOS, runtime.GOARCH)

	writeJSON(w, http.StatusOK, map[string]any{
		"available":        available,
		"path":             browserPath,
		"managedDir":       managedDir,
		"managedPath":      managedPath,
		"managedInstalled": managedPath != "",
		"platform":         platform,
		"supported":        platformErr == nil,
	})
}

// postChromeInstall downloads and installs Chrome for Testing into ~/.interseptor/chrome.
func (h *settingsAPI) postChromeInstall(w http.ResponseWriter, r *http.Request) {
	chromeInstallMu.Lock()
	defer chromeInstallMu.Unlock()

	managedDir := chromemgr.Dir("")
	installer := newChromeInstaller()

	path, err := installer.Install(r.Context(), managedDir, nil, nil)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"path":    path,
		"message": "Chrome installed successfully",
	})
}
