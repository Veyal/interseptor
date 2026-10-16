package control

import (
	"net/http"
	"strconv"
	"time"
)

const (
	authTimelineDefaultWindow = 120 * time.Second
	authTimelineMaxWindow     = 600 * time.Second
	authTimelineDefaultFlows  = 40
	authTimelineMaxFlows      = 100
)

// authTimelineHandler is GET /api/flows/{id}/auth-timeline. Read-only: it only
// reads captured flows and returns fingerprints, never cookie values.
func (h *flowAPI) authTimelineHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	start, err := h.st.GetFlow(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	window := authTimelineDefaultWindow
	if v, err := strconv.Atoi(r.URL.Query().Get("windowSeconds")); err == nil && v > 0 {
		window = time.Duration(v) * time.Second
		if window > authTimelineMaxWindow {
			window = authTimelineMaxWindow
		}
	}
	limit := authTimelineDefaultFlows
	if v, err := strconv.Atoi(r.URL.Query().Get("max")); err == nil && v > 0 {
		limit = min(v, authTimelineMaxFlows)
	}
	chain, err := authTimelineChain(h.st, start, window, limit)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildAuthTimeline(chain))
}
