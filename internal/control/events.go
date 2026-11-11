package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// sseSub is one subscriber's delivery state: its queue plus a one-slot signal
// that broadcast raises when it had to drop a message because the queue was full.
type sseSub struct {
	ch  chan string
	lag chan struct{} // nil for a channel registered without handleEvents (tests)
}

// broadcast marshals v and fans it out to every connected SSE client, dropping
// the message for any client whose buffer is full (slow consumer). A dropped
// message raises that client's lag signal, so its stream handler tells it to
// resync once the queue has drained (see handleEvents) instead of leaving it
// silently out of date.
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
	subs := make([]sseSub, 0, len(h.clients))
	for ch := range h.clients {
		subs = append(subs, sseSub{ch: ch, lag: h.clientLag[ch]})
	}
	h.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.ch <- s:
		default:
			if sub.lag != nil {
				select {
				case sub.lag <- struct{}{}:
				default: // already signalled
				}
			}
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

// sseWriteTimeout bounds each SSE write so a client that stops reading cannot
// block its handler (and server Shutdown) forever. A var so tests can shorten it.
var sseWriteTimeout = 30 * time.Second

// sseHeartbeatInterval is how often an otherwise idle stream writes a comment
// line. Without it a quiet stream sends no bytes, so a reverse proxy or share
// tunnel closes it as idle (Cloudflare: ~100 s) and neither side can tell a dead
// stream from a quiet one. Comment lines are ignored by EventSource. A var so
// tests can shorten it.
var sseHeartbeatInterval = 25 * time.Second

// sseResyncMinGap is the least time between two resync markers on one stream.
// The marker makes the client refetch every panel, so under a sustained flood
// (the queue overflowing again and again) it must stay rare.
var sseResyncMinGap = 5 * time.Second

// sseHeartbeatFrame is an SSE comment: no event is dispatched to the client.
const sseHeartbeatFrame = ": ping\n\n"

// sseStream writes Server-Sent Events frames with a per-write deadline.
type sseStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newSSEStream(w http.ResponseWriter) *sseStream {
	return &sseStream{w: w, rc: http.NewResponseController(w)}
}

// send writes one frame and flushes it, failing once the client stalls past
// sseWriteTimeout or disconnects.
func (s *sseStream) send(frame string) error {
	if err := s.rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := io.WriteString(s.w, frame); err != nil {
		return err
	}
	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	// Clear the deadline so it cannot fire on a later response if the
	// connection is reused after the stream ends.
	_ = s.rc.SetWriteDeadline(time.Time{})
	return nil
}

// handleEvents serves the Server-Sent Events stream.
func (h *metaAPI) handleEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ch := make(chan string, 64)
	lag := make(chan struct{}, 1)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	if h.clientLag == nil {
		h.clientLag = make(map[chan string]chan struct{})
	}
	h.clientLag[ch] = lag
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		delete(h.clientLag, ch)
		h.mu.Unlock()
	}()

	stream := newSSEStream(w)
	if stream.send(h.helloFrame(false)) != nil {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	ctx := r.Context()
	lagged := false
	var lastResync time.Time
	var resyncDue <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.streamStop:
			return
		case <-lag:
			lagged = true
		case <-resyncDue:
			resyncDue = nil
		case <-heartbeat.C:
			if stream.send(sseHeartbeatFrame) != nil {
				return
			}
		case msg := <-ch:
			if stream.send("data: "+msg+"\n\n") != nil {
				return
			}
		}
		// Events were dropped for this client while its queue was full. Once the
		// queue has drained, say so exactly once: everything queued before the
		// marker is stale-safe, everything after it is live again.
		// The marker makes the client refetch, so a client that keeps falling
		// behind gets at most one per sseResyncMinGap.
		if lagged && len(ch) == 0 {
			if wait := sseResyncMinGap - time.Since(lastResync); wait > 0 {
				if resyncDue == nil {
					resyncDue = time.After(wait)
				}
				continue
			}
			lagged, lastResync = false, time.Now()
			if stream.send(h.helloFrame(true)) != nil {
				return
			}
		}
	}
}

// helloFrame is the named `hello` event that opens a stream, followed by the
// current intercept snapshot (with its Seq) so a late joiner is correct from the
// stream alone and has the baseline the per-item deltas reconcile against —
// rather than depending on a separate REST fetch to arrive first.
//
// With resync it is sent again mid-stream after this client lost events to a
// full queue. The UI treats every `hello` after its first as a reconnect and
// refetches everything a gap could have made stale (app.js connectEvents), which
// is exactly the recovery a dropped event needs; the data carries {"resync":true}
// for clients that want to tell the two apart.
func (h *Hub) helloFrame(resync bool) string {
	frame := "event: hello\ndata: {}\n\n"
	if resync {
		frame = "event: hello\ndata: {\"resync\":true}\n\n"
	}
	if h.eng != nil {
		if b, err := json.Marshal(map[string]any{"type": "intercept.state", "intercept": h.interceptStateSummary()}); err == nil {
			frame += fmt.Sprintf("data: %s\n\n", b)
		}
	}
	return frame
}
