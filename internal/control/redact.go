package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/redact"
)

const maxRedactRequestBytes = 64 << 10

// redactValue describes a secret as {len, sha256_prefix, kind}. The value is
// hashed in memory and never stored, logged or echoed.
func (h *metaAPI) redactValue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value string `json:"value"`
	}
	if !decodeLimitedJSON(w, r, maxRedactRequestBytes, &in) {
		return
	}
	if in.Value == "" {
		httpErr(w, http.StatusBadRequest, "value is required (a non-empty string to describe)")
		return
	}
	writeJSON(w, http.StatusOK, redact.Describe(in.Value))
}
