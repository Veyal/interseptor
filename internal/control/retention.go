package control

import (
	"log"
	"net/http"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// purgeRequest is the request body for POST /api/flows/purge.
type purgeRequest struct {
	Hosts []string `json:"hosts"`
	Mode  string   `json:"mode"` // "delete" or "keepOnly"
	// DryRun reports what the prune would remove without deleting anything.
	DryRun bool `json:"dryRun"`
	// Filters narrow the selection; every non-empty one is ANDed with the hosts.
	Methods      []string `json:"methods"`
	Statuses     []int    `json:"statuses"`
	MimePrefixes []string `json:"mimePrefixes"`
	PathPrefixes []string `json:"pathPrefixes"`
	// UndoWindowMs overrides how long the batch stays restorable.
	UndoWindowMs int64 `json:"undoWindowMs"`
}

// purgeFlows removes flows matching host patterns and reclaims orphaned bodies.
//
//	POST /api/flows/purge
//	{"hosts":["acme.com","*.example.com"],"mode":"delete"|"keepOnly"}
//
// Response: {"deleted":<int>,"removedFiles":<int>,"freedBytes":<int>}
func (h *flowAPI) purgeFlows(w http.ResponseWriter, r *http.Request) {
	var in purgeRequest
	if !decodeLimitedJSON(w, r, maxBulkRequestBytes, &in) {
		return
	}
	if len(in.Hosts) > maxBulkItems {
		httpErr(w, http.StatusBadRequest, "too many hosts")
		return
	}
	switch in.Mode {
	case "delete", "keepOnly":
	default:
		httpErr(w, http.StatusBadRequest, "mode must be delete or keepOnly")
		return
	}

	if in.DryRun {
		// A dry run must not delete, broadcast or kick off a GC: it reports the
		// exact counts and bytes so the operator can decide before committing.
		res, err := h.st.PruneFlows(h.pruneOptions(in))
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		res.DryRun = true
		writeJSON(w, http.StatusOK, res)
		return
	}
	res, err := h.st.PruneFlows(h.pruneOptions(in))
	if err != nil {
		// store returns an error for keepOnly + empty hosts; surface as 400.
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	deleted := res.Deleted

	if deleted > 0 {
		h.epsCache.invalidate()
		h.broadcast(map[string]any{"type": "flow.new"}) // reuse the reload signal
	}
	// Reclaim orphaned body files in the background: a large bodies directory can
	// take seconds to walk (worse on Windows), and the user-visible delete is
	// already done. (The explicit "Reclaim space" button — gcBodies — stays sync.)
	h.startMaintenance(func() {
		if _, _, err := h.gcBodiesFn(); err != nil {
			log.Printf("purge GC: %v", err)
		}
	})

	res.Deleted = deleted
	writeJSON(w, http.StatusOK, res)
}

// pruneOptions maps the request body onto the store's prune selection.
func (h *flowAPI) pruneOptions(in purgeRequest) store.PruneOptions {
	op := store.PruneOptions{
		Hosts:        in.Hosts,
		KeepOnly:     in.Mode == "keepOnly",
		DryRun:       in.DryRun,
		Methods:      in.Methods,
		Statuses:     in.Statuses,
		MimePrefixes: in.MimePrefixes,
		PathPrefixes: in.PathPrefixes,
	}
	if in.UndoWindowMs > 0 {
		op.UndoWindow = time.Duration(in.UndoWindowMs) * time.Millisecond
	}
	return op
}

// listPendingPrunes reports which prune batches are still restorable.
//
//	GET /api/flows/prune/pending
func (h *flowAPI) listPendingPrunes(w http.ResponseWriter, r *http.Request) {
	items, err := h.st.PendingPrunes()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prunes": items})
}

// undoPrune restores one pruned batch.
//
//	POST /api/flows/prune/undo {"pruneId":N}
func (h *flowAPI) undoPrune(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PruneID int64 `json:"pruneId"`
	}
	if !decodeLimitedJSON(w, r, maxBulkRequestBytes, &in) {
		return
	}
	if in.PruneID <= 0 {
		httpErr(w, http.StatusBadRequest, "pruneId is required")
		return
	}
	restored, err := h.st.RestorePrune(in.PruneID)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.epsCache.invalidate()
	h.broadcast(map[string]any{"type": "flow.new"})
	writeJSON(w, http.StatusOK, map[string]any{"restored": restored, "pruneId": in.PruneID})
}

func (h *Hub) startMaintenance(run func()) bool {
	h.maintenanceMu.Lock()
	if h.maintenanceClosed {
		h.maintenanceMu.Unlock()
		return false
	}
	h.maintenanceWG.Add(1)
	h.maintenanceMu.Unlock()
	go func() {
		defer h.maintenanceWG.Done()
		run()
	}()
	return true
}

func (h *Hub) stopMaintenance() {
	h.maintenanceMu.Lock()
	h.maintenanceClosed = true
	h.maintenanceMu.Unlock()
	h.maintenanceWG.Wait()
}

// gcBodies reclaims orphaned body files without deleting any flows.
//
//	POST /api/flows/gc
//
// Response: {"removedFiles":<int>,"freedBytes":<int>}
func (h *flowAPI) gcBodies(w http.ResponseWriter, r *http.Request) {
	removedFiles, freedBytes, err := h.st.GCBodies()
	if err != nil {
		httpInternalErr(w, err)
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
func (h *flowAPI) hostStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.st.HostStats()
	if err != nil {
		httpInternalErr(w, err)
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
