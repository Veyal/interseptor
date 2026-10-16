package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Veyal/interseptor/internal/collrun"
)

// Asynchronous runner API. A run is owned by the collrun.Manager, not by the
// HTTP request that started it: the UI runner view starts one, then follows it
// by polling /status, by the per-run SSE stream, or by the global event stream
// ({type:"collrun", event}). Everything shown is already masked by the
// backend's single scrubber.

func (c *collectionsAPI) startAsync(w http.ResponseWriter, r *http.Request) {
	var in runRequest
	if !decodeLimitedJSON(w, r, maxCollectionJSONBytes, &in) {
		return
	}
	opt, ok := c.runOptions(w, r, in, true)
	if !ok {
		return
	}
	lr, err := c.startRun(opt)
	if err != nil {
		startErr(w, err)
		return
	}
	p := lr.Snapshot(0)
	c.writeRunJSON(w, r, http.StatusAccepted, map[string]any{"runUid": lr.UID, "status": p.Status, "plannedSteps": p.Planned})
}

func (c *collectionsAPI) writeRunJSON(w http.ResponseWriter, r *http.Request, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func (c *collectionsAPI) liveRun(w http.ResponseWriter, r *http.Request) (*collrun.LiveRun, bool) {
	lr, ok := c.mgr.Get(r.PathValue("uid"))
	if !ok {
		httpErr(w, http.StatusNotFound, "not found")
		return nil, false
	}
	return lr, true
}

func (c *collectionsAPI) activeRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"active": c.mgr.Active()})
}

func (c *collectionsAPI) runStatus(w http.ResponseWriter, r *http.Request) {
	lr, ok := c.liveRun(w, r)
	if !ok {
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	writeJSON(w, http.StatusOK, lr.Snapshot(since))
}

func (c *collectionsAPI) runPause(w http.ResponseWriter, r *http.Request) {
	if lr, ok := c.liveRun(w, r); ok {
		lr.Pause()
		writeJSON(w, http.StatusOK, map[string]any{"status": lr.Snapshot(0).Status})
	}
}

func (c *collectionsAPI) runResume(w http.ResponseWriter, r *http.Request) {
	if lr, ok := c.liveRun(w, r); ok {
		lr.Resume()
		writeJSON(w, http.StatusOK, map[string]any{"status": lr.Snapshot(0).Status})
	}
}

func (c *collectionsAPI) runAbort(w http.ResponseWriter, r *http.Request) {
	if lr, ok := c.liveRun(w, r); ok {
		lr.Abort()
		writeJSON(w, http.StatusOK, map[string]any{"status": "aborting"})
	}
}

// runPersist answers a persist=ask prompt. Keeping script variable writes is a
// state change owned by a person, so it is UI-session only like script trust.
func (c *collectionsAPI) runPersist(w http.ResponseWriter, r *http.Request) {
	if err := requireUISession(r); err != nil {
		httpErr(w, http.StatusForbidden, err.Error())
		return
	}
	lr, ok := c.liveRun(w, r)
	if !ok {
		return
	}
	var in struct {
		Keep bool `json:"keep"`
	}
	if !decodeLimitedJSON(w, r, maxCollectionSmallBytes, &in) {
		return
	}
	if !lr.Decide(in.Keep) {
		httpErr(w, http.StatusConflict, "the run is not waiting for a persist decision")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decision": map[bool]string{true: "keep", false: "discard"}[in.Keep]})
}

// runEvents streams one run as SSE: a snapshot first (so a late attach never
// misses results), then every event until the run ends.
func (c *collectionsAPI) runEvents(w http.ResponseWriter, r *http.Request) {
	lr, ok := c.liveRun(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Subscribe before the snapshot: an event in between is a harmless
	// duplicate (items carry their sequence number), never a gap.
	ch, cancel := lr.Subscribe()
	defer cancel()
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	send(map[string]any{"type": "snapshot", "progress": lr.Snapshot(since)})
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			send(e)
		}
	}
}
