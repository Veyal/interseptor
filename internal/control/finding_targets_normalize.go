package control

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Veyal/interseptor/internal/store"
)

// normalizeFindingTargets previews (default) or applies reviewer-approved path
// templates to one saved finding. Approvals are suggestion indexes returned by
// the preview; nothing is persisted unless dryRun is explicitly false.
func (h *findingsAPI) normalizeFindingTargets(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Approve []int `json:"approve"`
		DryRun  *bool `json:"dryRun"`
	}
	if !decodeOptionalLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	p, err := store.NormalizeFindingTargets(f.Targets, "", in.Approve)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dryRun := in.DryRun == nil || *in.DryRun
	if !dryRun {
		patch := store.FindingMetadataPatch{Change: findingAPIChange("normalize targets"), Targets: &p.Targets}
		if err := h.st.UpdateFindingMetadata(id, patch); err != nil {
			if errors.Is(err, store.ErrInvalidFinding) || errors.Is(err, store.ErrFlowNotFound) {
				httpErr(w, http.StatusBadRequest, err.Error())
				return
			}
			httpInternalErr(w, err)
			return
		}
		h.broadcast(map[string]any{"type": "findings.update"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dryRun": dryRun, "applied": !dryRun, "preview": p})
}
