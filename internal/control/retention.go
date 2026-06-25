package control

import (
	"encoding/json"
	"net/http"
)

// purgeRequest is the request body for POST /api/flows/purge.
type purgeRequest struct {
	Hosts []string `json:"hosts"`
	Mode  string   `json:"mode"` // "delete" or "keepOnly"
}

// purgeFlows removes flows matching host patterns and reclaims orphaned bodies.
//
//	POST /api/flows/purge
//	{"hosts":["acme.com","*.example.com"],"mode":"delete"|"keepOnly"}
//
// Response: {"deleted":<int>,"removedFiles":<int>,"freedBytes":<int>}
func (h *Hub) purgeFlows(w http.ResponseWriter, r *http.Request) {
	var in purgeRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	keepOnly := in.Mode == "keepOnly"

	deleted, err := h.st.DeleteFlowsByHost(in.Hosts, keepOnly)
	if err != nil {
		// store returns an error for keepOnly + empty hosts; surface as 400.
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}

	removedFiles, freedBytes, err := h.st.GCBodies()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "gc: "+err.Error())
		return
	}

	if deleted > 0 {
		h.broadcast(map[string]any{"type": "flow.new"}) // reuse the reload signal
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":      deleted,
		"removedFiles": removedFiles,
		"freedBytes":   freedBytes,
	})
}

// gcBodies reclaims orphaned body files without deleting any flows.
//
//	POST /api/flows/gc
//
// Response: {"removedFiles":<int>,"freedBytes":<int>}
func (h *Hub) gcBodies(w http.ResponseWriter, r *http.Request) {
	removedFiles, freedBytes, err := h.st.GCBodies()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removedFiles": removedFiles,
		"freedBytes":   freedBytes,
	})
}

// hostStats returns per-host flow counts and byte totals.
//
//	GET /api/hosts/stats
//
// Response: {"hosts":[{"host":...,"flows":...,"bytes":...}],"totalFlows":...,"totalBytes":...}
func (h *Hub) hostStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.st.HostStats()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	type hostJSON struct {
		Host  string `json:"host"`
		Flows int64  `json:"flows"`
		Bytes int64  `json:"bytes"`
	}

	hosts := make([]hostJSON, 0, len(stats))
	var totalFlows, totalBytes int64
	for _, hs := range stats {
		hosts = append(hosts, hostJSON{Host: hs.Host, Flows: hs.Flows, Bytes: hs.Bytes})
		totalFlows += hs.Flows
		totalBytes += hs.Bytes
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hosts":      hosts,
		"totalFlows": totalFlows,
		"totalBytes": totalBytes,
	})
}
