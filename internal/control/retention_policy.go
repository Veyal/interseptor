package control

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Automatic retention: delete flows older than maxAgeHours, and keep only the
// most-recent maxFlows. Either at 0 = that lever is off. Runs on a background
// ticker (so a long engagement can't fill the disk silently) and on demand via
// POST /api/flows/retention/run.
const (
	retentionMaxAgeHoursKey = "retention.maxAgeHours"
	retentionMaxFlowsKey    = "retention.maxFlows"
	retentionInterval       = 30 * time.Minute
)

type retentionPolicy struct {
	MaxAgeHours int64 `json:"maxAgeHours"`
	MaxFlows    int64 `json:"maxFlows"`
}

func (h *Hub) loadRetentionPolicy() retentionPolicy {
	p := retentionPolicy{}
	if v, ok, _ := h.st.GetSetting(retentionMaxAgeHoursKey); ok {
		p.MaxAgeHours, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok, _ := h.st.GetSetting(retentionMaxFlowsKey); ok {
		p.MaxFlows, _ = strconv.ParseInt(v, 10, 64)
	}
	return p
}

// runRetentionOnce applies the configured retention policy and reclaims orphaned
// bodies. Returns the number of flows deleted. Safe to call from a goroutine.
func (h *Hub) runRetentionOnce() (int64, error) {
	p := h.loadRetentionPolicy()
	var deleted int64
	if p.MaxAgeHours > 0 {
		cutoff := time.Now().Add(-time.Duration(p.MaxAgeHours) * time.Hour).UnixMilli()
		n, err := h.st.DeleteFlowsOlderThan(cutoff)
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	if p.MaxFlows > 0 {
		n, err := h.st.DeleteFlowsKeepNewest(p.MaxFlows)
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	if deleted > 0 {
		h.epsCache.invalidate()
		h.broadcast(map[string]any{"type": "flow.new"})
		go func() {
			if _, _, err := h.st.GCBodies(); err != nil {
				log.Printf("retention GC: %v", err)
			}
		}()
	}
	return deleted, nil
}

// StartRetentionLoop runs runRetentionOnce on a ticker until stop is closed.
// Non-blocking; logs failures and keeps ticking.
func (h *Hub) StartRetentionLoop(stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(retentionInterval)
		defer t.Stop()
		// One run shortly after start so a long-idle install cleans up on launch.
		time.Sleep(10 * time.Second)
		h.runRetentionTick()
		for {
			select {
			case <-t.C:
				h.runRetentionTick()
			case <-stop:
				return
			}
		}
	}()
}

func (h *Hub) runRetentionTick() {
	if _, err := h.runRetentionOnce(); err != nil {
		log.Printf("retention: %v", err)
	}
}

func (h *flowAPI) getRetention(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.loadRetentionPolicy())
}

func (h *flowAPI) putRetention(w http.ResponseWriter, r *http.Request) {
	var in retentionPolicy
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.MaxAgeHours < 0 || in.MaxFlows < 0 {
		httpErr(w, http.StatusBadRequest, "maxAgeHours and maxFlows must be >= 0 (0 = off)")
		return
	}
	if !h.persistSetting(w, retentionMaxAgeHoursKey, strconv.FormatInt(in.MaxAgeHours, 10)) {
		return
	}
	if !h.persistSetting(w, retentionMaxFlowsKey, strconv.FormatInt(in.MaxFlows, 10)) {
		return
	}
	writeJSON(w, http.StatusOK, h.loadRetentionPolicy())
}

func (h *flowAPI) runRetention(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.runRetentionOnce()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
