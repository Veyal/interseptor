package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// FlowCaptured implements proxy.Events: it pushes a newly-seen flow (request
// sent, response not yet known) to all live UI subscribers. Concurrency-safe.
func (h *Hub) FlowCaptured(f *store.Flow) {
	h.epsCache.invalidateDebounced()
	h.broadcast(map[string]any{"type": "flow.new", "flow": toFlowJSON(f)})
}

// FlowUpdated implements proxy.Events: it pushes the filled-in flow (response or
// error now known) so the UI can update the existing history row in place.
func (h *Hub) FlowUpdated(f *store.Flow) {
	h.epsCache.invalidateDebounced()
	h.broadcast(map[string]any{"type": "flow.update", "flow": toFlowJSON(f)})
}

// WSFramed implements the proxy's optional ws-frame sink: it nudges the UI to
// refresh the frame list for a websocket flow. Events are debounced per flow.
func (h *Hub) WSFramed(flowID int64) {
	h.wsMu.Lock()
	if h.wsTimers == nil {
		h.wsTimers = make(map[int64]*time.Timer)
	}
	if t, ok := h.wsTimers[flowID]; ok {
		t.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(200*time.Millisecond, func() {
		h.wsMu.Lock()
		// Only clean up if we're still the current timer — a fired-but-superseded
		// timer must not delete the map entry a newer WSFramed just installed.
		if h.wsTimers[flowID] == t {
			delete(h.wsTimers, flowID)
		}
		h.wsMu.Unlock()
		h.broadcast(map[string]any{"type": "ws.frame", "flowId": flowID})
	})
	h.wsTimers[flowID] = t
	h.wsMu.Unlock()
}

// broadcastIntercept pushes the full intercept snapshot (toggles + both hold
// queues + Seq) as intercept.update. It is the bulk/reset path: the per-item
// hold and release changes go out as small deltas (see onInterceptChange).
func (h *Hub) broadcastIntercept() {
	h.broadcast(map[string]any{"type": "intercept.update", "intercept": h.interceptStateSummary()})
}

// onInterceptChange is the intercept engine's typed change notifier. A per-item
// hold or release becomes a small delta carrying only the affected item and the
// generation Seq the client reconciles against; a bulk reset (intercept toggled,
// queue drained) re-sends the whole snapshot. This replaces re-transmitting the
// entire hold queue on every event. It may be invoked concurrently and never
// blocks forwarding (the engine fires it outside its lock).
func (h *Hub) onInterceptChange(c intercept.Change) {
	switch c.Kind {
	case intercept.ChangeHeld:
		var item heldJSON
		if c.Side == intercept.SideResponse {
			item = heldFromResponse(c.Response, false)
		} else {
			item = heldFromRequest(c.Request, false)
		}
		h.broadcast(map[string]any{"type": "intercept.held", "seq": c.Seq, "side": c.Side, "item": item})
	case intercept.ChangeReleased:
		h.broadcast(map[string]any{"type": "intercept.released", "seq": c.Seq, "side": c.Side, "id": c.ID, "reason": c.Reason})
	default: // intercept.ChangeReset
		h.broadcastIntercept()
	}
}

// broadcast marshals v and fans it out to every connected SSE client, dropping
// the message for any client whose buffer is full (slow consumer).
func (h *Hub) broadcast(v any) {
	msg, err := json.Marshal(v)
	if err != nil {
		return
	}
	s := string(msg)
	// Snapshot the subscriber channels under a short lock, then send outside it so
	// the fan-out doesn't hold h.mu against client connect/disconnect. Sends stay
	// non-blocking; channels are only removed from the map (never closed), so a send
	// racing an unsubscribe just lands in a buffer nobody reads — harmless.
	h.mu.Lock()
	chans := make([]chan string, 0, len(h.clients))
	for ch := range h.clients {
		chans = append(chans, ch)
	}
	h.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- s:
		default:
		}
	}
}

// CloseStreams ends every open event stream (UI /api/events and collection run
// streams). Call it before shutting the control server down: Shutdown waits
// for active connections, and a stream only ends when its handler returns.
// Idempotent; Close calls it too.
func (h *Hub) CloseStreams() {
	h.streamStopOnce.Do(func() {
		if h.streamStop != nil { // nil only for a Hub literal built without New
			close(h.streamStop)
		}
	})
}

// handleEvents serves the Server-Sent Events stream.
func (h *metaAPI) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()

	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	// Give a newly connected client the current intercept snapshot (with its Seq)
	// as the first frame, so a late joiner is correct from the stream alone and has
	// the baseline the per-item deltas reconcile against — rather than depending on
	// a separate REST fetch to arrive first.
	if h.eng != nil {
		if b, err := json.Marshal(map[string]any{"type": "intercept.state", "intercept": h.interceptStateSummary()}); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.streamStop:
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
