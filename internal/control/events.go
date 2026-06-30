package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Veyal/interceptor/internal/store"
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

// broadcastIntercept pushes the current intercept state (toggle + hold queue).
// It is registered as the intercept engine's change notifier and may be invoked
// concurrently.
func (h *Hub) broadcastIntercept() {
	h.broadcast(map[string]any{"type": "intercept.update", "intercept": h.interceptStateSummary()})
}

// broadcast marshals v and fans it out to every connected SSE client, dropping
// the message for any client whose buffer is full (slow consumer).
func (h *Hub) broadcast(v any) {
	msg, err := json.Marshal(v)
	if err != nil {
		return
	}
	s := string(msg)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- s:
		default:
		}
	}
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
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
