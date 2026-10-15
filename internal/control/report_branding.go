package control

import (
	"errors"
	"net/http"

	"github.com/Veyal/interseptor/internal/store"
)

// maxReportBrandingRequestBytes allows two embedded logos, the cover page and
// the single-line fields with room to spare.
const maxReportBrandingRequestBytes int64 = 2*store.MaxReportLogoBytes + store.MaxReportCoverPageBytes + 64<<10

// getReportBranding returns the project's white-label report settings. An unset
// project returns the zero value at version 0, which renders the default report.
func (h *projectAPI) getReportBranding(w http.ResponseWriter, r *http.Request) {
	b, err := h.st.GetReportBranding()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// putReportBranding replaces the white-label settings. Every field is optional;
// an empty body resets the project to the default report. The response is the
// stored document, including the version an export will cite.
func (h *projectAPI) putReportBranding(w http.ResponseWriter, r *http.Request) {
	var in store.ReportBranding
	if !decodeLimitedJSON(w, r, maxReportBrandingRequestBytes, &in) {
		return
	}
	b, err := h.st.SetReportBranding(in)
	if err != nil {
		if errors.Is(err, store.ErrInvalidEngagement) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "reportBranding.update"})
	writeJSON(w, http.StatusOK, b)
}
