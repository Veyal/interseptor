package control

import (
	"github.com/Veyal/interseptor/internal/store"
	"net/http"
	"net/url"
	"strconv"
)

func findingAPIChange(reason string) store.FindingChange {
	return store.FindingChange{Actor: "API client", Source: "http", Reason: reason}
}
func findingRevisionID(w http.ResponseWriter, raw, name string, optional bool) (int64, bool) {
	if optional && raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 || (!optional && id == 0) {
		httpErr(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func (h *findingsAPI) findingRevisions(w http.ResponseWriter, r *http.Request) {
	id, ok := findingRevisionID(w, r.PathValue("id"), "finding id", false)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["before"]) > 1 {
		httpErr(w, http.StatusBadRequest, "invalid before cursor")
		return
	}
	before, ok := findingRevisionID(w, query.Get("before"), "before cursor", true)
	if !ok {
		return
	}
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
	id, ok := findingRevisionID(w, r.PathValue("id"), "finding id", false)
	if !ok {
		return
	}
	revisionID, ok := findingRevisionID(w, r.PathValue("revisionId"), "revision id", false)
	if !ok {
		return
	}
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
	id, ok := findingRevisionID(w, r.PathValue("id"), "finding id", false)
	if !ok {
		return
	}
	revisionID, ok := findingRevisionID(w, r.PathValue("revisionId"), "revision id", false)
	if !ok {
		return
	}
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
