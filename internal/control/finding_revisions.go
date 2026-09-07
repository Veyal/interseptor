package control

import (
	"github.com/Veyal/interseptor/internal/store"
	"net/http"
	"strconv"
)

func findingAPIChange(reason string) store.FindingChange {
	return store.FindingChange{Actor: "API client", Source: "http", Reason: reason}
}
func (h *findingsAPI) findingRevisions(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	revisions, err := h.st.ListFindingRevisionsPage(id, before, 100)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	var next int64
	if len(revisions) == 100 {
		next = revisions[len(revisions)-1].ID
	}
	writeJSON(w, 200, map[string]any{"revisions": revisions, "nextBefore": next})
}
func (h *findingsAPI) findingRevision(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	revisionID, _ := strconv.ParseInt(r.PathValue("revisionId"), 10, 64)
	revision, diff, err := h.st.GetFindingRevision(id, revisionID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "revision not found")
		return
	}
	writeJSON(w, 200, map[string]any{"revision": revision, "diff": diff})
}
func (h *findingsAPI) deletedFindings(w http.ResponseWriter, r *http.Request) {
	revisions, err := h.st.DeletedFindings(100)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revisions": revisions})
}
func (h *findingsAPI) restoreFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	revisionID, _ := strconv.ParseInt(r.PathValue("revisionId"), 10, 64)
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalLimitedJSON(w, r, 4096, &in) {
		return
	}
	if err := h.st.RestoreFindingRevision(id, revisionID, findingAPIChange(in.Reason)); err != nil {
		httpNotFoundOrInternal(w, err, "revision not found")
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, 200, findingAPIResponse(f, nil))
}
