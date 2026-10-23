package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/version"
)

// SetUpdate records the result of the background update check (called by cmd) so
// it can be served at GET /api/version and surfaced in the UI.
func (h *Hub) SetUpdate(latest string, available bool) {
	h.updMu.Lock()
	h.updLatest, h.updAvail = latest, available
	h.updMu.Unlock()
}

// apiVersion reports the running version and (once the background check has run)
// whether a newer release is available.
func (h *metaAPI) apiVersion(w http.ResponseWriter, r *http.Request) {
	h.updMu.Lock()
	latest, avail := h.updLatest, h.updAvail
	h.updMu.Unlock()
	method := ""
	if h.Updater != nil {
		method = string(h.Updater.Install().Method)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installMethod":   method, // "desktop": the native macOS app runs and updates this server
		"version":         version.String(),
		"latest":          latest,
		"updateAvailable": avail,
		"repo":            version.Repo,
		"project":         h.ProjectName,
		"projectDir":      h.ProjectDir,
	})
}
