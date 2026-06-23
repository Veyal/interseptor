package control

import (
	"net/http"

	"github.com/Veyal/interceptor/internal/version"
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
func (h *Hub) apiVersion(w http.ResponseWriter, r *http.Request) {
	h.updMu.Lock()
	latest, avail := h.updLatest, h.updAvail
	h.updMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         version.String(),
		"latest":          latest,
		"updateAvailable": avail,
		"repo":            version.Repo,
	})
}
