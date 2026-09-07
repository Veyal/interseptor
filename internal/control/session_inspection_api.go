package control

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// inspectSession reads only already captured flow records. It intentionally has
// no sender/replay dependency: selecting a capture is the only operation that
// can populate this passive view.
func (h *flowAPI) inspectSession(w http.ResponseWriter, r *http.Request) {
	roleValues := append([]string{}, r.URL.Query()["roles"]...)
	roleValues = append(roleValues, r.URL.Query()["role"]...)
	var roles []string
	for _, raw := range roleValues {
		for _, role := range strings.Split(raw, ",") {
			if strings.TrimSpace(role) != "" {
				roles = append(roles, normalizeSessionRole(role))
			}
		}
	}
	ids, err := sessionInspectionIDs(len(roles) > 0, r.URL.Query()["ids"], r.URL.Query()["id"])
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(roles) > len(ids) {
		httpErr(w, http.StatusBadRequest, "more roles than selected flows")
		return
	}
	flows := make([]*store.Flow, 0, len(ids))
	for _, id := range ids {
		flow, err := h.st.GetFlow(id)
		if errors.Is(err, sql.ErrNoRows) {
			httpErr(w, http.StatusNotFound, "flow not found: "+strconv.FormatInt(id, 10))
			return
		}
		if err != nil {
			httpInternalErr(w, err)
			return
		}
		flows = append(flows, flow)
	}
	writeJSON(w, http.StatusOK, buildSessionInspection(flows, roles))
}

func sessionInspectionIDs(rejectDuplicates bool, groups ...[]string) ([]int64, error) {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, maxSessionInspectionFlows)
	for _, group := range groups {
		for _, raw := range group {
			for _, token := range strings.Split(raw, ",") {
				token = strings.TrimSpace(token)
				if token == "" {
					continue
				}
				id, err := strconv.ParseInt(token, 10, 64)
				if err != nil || id <= 0 {
					return nil, errors.New("ids must contain positive integers")
				}
				if _, ok := seen[id]; ok {
					if rejectDuplicates {
						return nil, errors.New("duplicate flow ids are not allowed when roles are supplied")
					}
					continue
				}
				if len(ids) >= maxSessionInspectionFlows {
					return nil, errors.New("select at most 32 flows")
				}
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("select at least one captured flow")
	}
	return ids, nil
}
