package control

import (
	"fmt"
	"net/http"
	"strings"
)

// authzDifferentialRun is POST /api/authz/differential.
func (h *authzAPI) authzDifferentialRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID           int64    `json:"flowId"`
		Identities       []string `json:"identities"`
		InvalidBody      string   `json:"invalidBody"`
		SideEffectFlowID int64    `json:"sideEffectFlowId"`
		AttachToFinding  int64    `json:"attachToFinding"`
	}
	if !decodeLimitedJSON(w, r, maxAuthzReplayRequestBytes, &in) {
		return
	}
	if in.FlowID <= 0 {
		httpErr(w, http.StatusBadRequest, "flowId required")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "flow not found")
		return
	}
	saved, err := h.authzIdentitiesResult()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	ids, err := selectDifferentialIdentities(saved, in.Identities)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := differentialOpts{InvalidBody: in.InvalidBody}
	if in.SideEffectFlowID > 0 {
		if opts.SideEffectFlow, err = h.st.GetFlow(in.SideEffectFlowID); err != nil {
			httpNotFoundOrInternal(w, err, "sideEffectFlowId flow not found")
			return
		}
	}
	if in.AttachToFinding > 0 {
		if _, err := h.st.GetFinding(in.AttachToFinding); err != nil {
			httpNotFoundOrInternal(w, err, "finding not found")
			return
		}
	}
	ev := h.authzDifferential(f, ids, opts)
	if in.AttachToFinding > 0 {
		if err := ev.AttachTo(h.st, in.AttachToFinding); err != nil {
			httpInternalErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, ev)
}

// selectDifferentialIdentities filters the saved identities by name. An empty
// selection means all; "anonymous" is always allowed (it is implicit).
func selectDifferentialIdentities(saved []identity, names []string) ([]identity, error) {
	if len(names) == 0 {
		return saved, nil
	}
	var out []identity
	for _, n := range names {
		n = strings.TrimSpace(n)
		if strings.EqualFold(n, anonymousContextName) {
			continue
		}
		found := false
		for _, id := range saved {
			if id.Name == n {
				out = append(out, id)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown identity %q", n)
		}
	}
	return out, nil
}
