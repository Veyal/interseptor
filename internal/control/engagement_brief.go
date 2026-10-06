package control

import (
	"errors"
	"net/http"

	"github.com/Veyal/interseptor/internal/store"
)

// A brief has six fields of at most store.MaxEngagementFieldBytes each.
const maxEngagementBriefRequestBytes int64 = 8 * store.MaxEngagementFieldBytes

// getEngagementBrief returns the project's authorisation/conduct brief
// (version 0 and empty fields when none has been recorded).
func (h *projectAPI) getEngagementBrief(w http.ResponseWriter, r *http.Request) {
	b, err := h.st.GetEngagementBrief()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// putEngagementBrief replaces the brief. The version increments only when the
// content changes, so re-saving an unchanged brief keeps its citation stable.
func (h *projectAPI) putEngagementBrief(w http.ResponseWriter, r *http.Request) {
	var in store.EngagementBrief
	if !decodeLimitedJSON(w, r, maxEngagementBriefRequestBytes, &in) {
		return
	}
	b, err := h.st.SetEngagementBrief(in)
	if err != nil {
		if errors.Is(err, store.ErrInvalidEngagement) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "engagement.update"})
	writeJSON(w, http.StatusOK, b)
}
