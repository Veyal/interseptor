package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

func (h *metaAPI) listAllowlist(w http.ResponseWriter, r *http.Request) {
	entries, err := h.st.ListIPAllowlist()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":  entries,
		"clientIP": clientIP(r),
	})
}

func (h *metaAPI) createAllowlist(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CIDR  string `json:"cidr"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	e, err := h.st.AddIPAllowlist(in.CIDR, in.Label)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "allowlist.update"})
	writeJSON(w, http.StatusOK, e)
}

func (h *metaAPI) deleteAllowlist(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteIPAllowlist(id); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "allowlist.update"})
	w.WriteHeader(http.StatusNoContent)
}
