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
	// chromeDir is where managed Chrome lives (--data-dir/INTERSEPTOR_DATA_DIR,
	// else ~/.interseptor/chrome); tests point it at a temp dir.
	chromeDir       = func() string { return chromemgr.Dir("") }
	chromeInstallMu sync.Mutex
)

// getChromeStatus reports whether Chrome/Chromium is installed and available,
// including managed Chrome in ~/.interseptor/chrome.
func (h *settingsAPI) getChromeStatus(w http.ResponseWriter, r *http.Request) {
	managedDir := chromeDir()
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

// postChromeInstall downloads and installs Chrome for Testing into the managed
// directory. One install runs at a time (a second request gets 409), and an
// existing install is reported instead of downloading ~150 MB again.
func (h *settingsAPI) postChromeInstall(w http.ResponseWriter, r *http.Request) {
	if !chromeInstallMu.TryLock() {
		httpErr(w, http.StatusConflict, "a Chrome install is already running")
		return
	}
	defer chromeInstallMu.Unlock()

	managedDir := chromeDir()
	if path, err := chromemgr.BinaryPath(managedDir, runtime.GOOS, runtime.GOARCH); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"already": true,
			"path":    path,
			"message": "Chrome is already installed",
		})
		return
	}

	path, err := newChromeInstaller().Install(r.Context(), managedDir, nil, nil)
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
