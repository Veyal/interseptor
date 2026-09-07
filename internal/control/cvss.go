package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/cvss"
)

const maxCVSSRequestBytes = 4 << 10

func (h *metaAPI) evaluateCVSS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Vector string `json:"vector"`
	}
	if !decodeLimitedJSON(w, r, maxCVSSRequestBytes, &in) {
		return
	}
	evaluation, err := cvss.Evaluate(in.Vector)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, evaluation)
}
