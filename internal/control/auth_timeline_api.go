package control

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
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
	if v, ok := positiveIntParam(r.URL.Query().Get("windowSeconds")); ok {
		// Compare in seconds: converting first overflows time.Duration for a
		// large value and turned the cap into a zero-length window.
		window = authTimelineMaxWindow
		if v < int64(authTimelineMaxWindow/time.Second) {
			window = time.Duration(v) * time.Second
		}
	}
	limit := authTimelineDefaultFlows
	if v, ok := positiveIntParam(r.URL.Query().Get("max")); ok {
		limit = int(min(v, authTimelineMaxFlows))
	}
	chain, err := authTimelineChain(h.st, start, window, limit)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildAuthTimeline(chain))
}

// positiveIntParam parses a positive decimal count. A value too large for
// int64 is still a (very large) positive count, so the caller's cap applies
// to it rather than the default.
func positiveIntParam(raw string) (int64, bool) {
	v, err := strconv.ParseInt(raw, 10, 64)
	if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(raw, "-") {
		return math.MaxInt64, true
	}
	return v, err == nil && v > 0
}
