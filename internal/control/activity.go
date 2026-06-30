package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

// recordActivity persists one AI (MCP) tool call (stamped with the server clock)
// and returns it with its assigned id. Persisted per-project so the glass-box
// feed survives restarts; the caller broadcasts it live.
func (h *metaAPI) recordActivity(a store.Activity) store.Activity {
	a.TS = time.Now().UnixMilli()
	_, _ = h.st.InsertActivity(&a)
	return a
}

// postActivity records one AI tool call (POSTed by the MCP server after every
// call), persists it, and pushes it to all live UI subscribers.
func (h *metaAPI) postActivity(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	var in struct {
		Tool    string `json:"tool"`
		Summary string `json:"summary"`
		OK      bool   `json:"ok"`
		Result  string `json:"result"`
		Ms      int64  `json:"ms"`
		Intent  string `json:"intent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Tool == "" {
		httpErr(w, http.StatusBadRequest, "tool required")
		return
	}
	it := h.recordActivity(store.Activity{Tool: in.Tool, Summary: in.Summary, OK: in.OK, Result: in.Result, Ms: in.Ms, Intent: in.Intent})
	h.broadcast(map[string]any{"type": "activity", "item": it})
	w.WriteHeader(http.StatusNoContent)
}

// listActivity returns the persisted AI activity, newest first.
func (h *metaAPI) listActivity(w http.ResponseWriter, r *http.Request) {
	if h.aiDisabled() {
		writeJSON(w, http.StatusOK, map[string]any{"activity": []store.Activity{}})
		return
	}
	items, err := h.st.ListActivity(atoiOr(r.URL.Query().Get("limit"), 300))
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.Activity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": items})
}

// clearActivity wipes the persisted AI activity feed (the user pressed Clear).
// Because the feed is now stored, a client-only clear would reappear on reload —
// this deletes the rows and tells live clients to drop their copy.
func (h *metaAPI) clearActivity(w http.ResponseWriter, r *http.Request) {
	if err := h.st.DeleteActivity(); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "activity.clear"})
	w.WriteHeader(http.StatusNoContent)
}
