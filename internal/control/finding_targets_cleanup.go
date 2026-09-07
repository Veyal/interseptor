package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/store"
)

func (h *findingsAPI) previewFindingTargets(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Targets store.FindingTargets `json:"targets"`
		Legacy  string               `json:"legacy"`
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	p, err := store.PreviewFindingTargets(in.Targets, in.Legacy)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, p)
}
