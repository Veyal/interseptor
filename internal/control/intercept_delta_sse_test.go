package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// waitEngQueue blocks until the engine's request hold queue reaches n items.
func waitEngQueue(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.eng.Snapshot().Queue) == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hold queue never reached %d (have %d)", n, len(h.eng.Snapshot().Queue))
}

// waitEvent polls the collected SSE events until one matches pred, or fails.
func waitEvent(t *testing.T, events func() []map[string]any, what string, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range events() {
			if pred(m) {
				return m
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("SSE event %q never arrived; got %+v", what, events())
	return nil
}

func typeIs(m map[string]any, t string) bool { s, _ := m["type"].(string); return s == t }

func newHeldRequest(t *testing.T, path string) (*http.Request, []byte, *store.Flow) {
	t.Helper()
	r, err := http.NewRequest("GET", "https://example.com"+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	raw := []byte("GET " + path + " HTTP/1.1\r\nHost: example.com\r\n\r\n")
	return r, raw, &store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: path}
}

// A hold and a forward must travel as per-item deltas (intercept.held /
// intercept.released) carrying only the affected item and a generation seq —
// NOT a re-broadcast of the whole queue on every event (the O(N^2) fix).
func TestInterceptHoldForwardEmitDeltaSSE(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	events, ready, stop := collectEvents(t, ts.URL)
	defer stop()
	ready()

	eng.SetEnabled(true) // one reset snapshot (intercept.update)

	req, raw, flow := newHeldRequest(t, "/delta")
	go eng.Hold(flow, req, raw)
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID

	held := waitEvent(t, events, "intercept.held", func(m map[string]any) bool {
		return typeIs(m, "intercept.held")
	})
	if side, _ := held["side"].(string); side != "req" {
		t.Fatalf("held side = %v, want req", held["side"])
	}
	item, _ := held["item"].(map[string]any)
	if item == nil || int64(item["id"].(float64)) != id {
		t.Fatalf("held item missing id %d: %+v", id, held["item"])
	}
	if item["host"] != "example.com" || item["path"] != "/delta" || item["method"] != "GET" {
		t.Fatalf("held item summary wrong: %+v", item)
	}
	if _, hasRaw := item["raw"]; hasRaw {
		t.Error("the delta item must be bounded: no raw body bytes in the SSE frame")
	}
	heldSeq, ok := held["seq"].(float64)
	if !ok {
		t.Fatalf("held delta missing numeric seq: %+v", held)
	}

	if err := eng.Forward(id, nil); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	rel := waitEvent(t, events, "intercept.released", func(m map[string]any) bool {
		return typeIs(m, "intercept.released")
	})
	if int64(rel["id"].(float64)) != id {
		t.Fatalf("released id = %v, want %d", rel["id"], id)
	}
	if rel["reason"] != "forwarded" || rel["side"] != "req" {
		t.Fatalf("released delta = %+v, want forwarded/req", rel)
	}
	if relSeq := rel["seq"].(float64); relSeq != heldSeq+1 {
		t.Fatalf("released seq %v must be held seq %v + 1", relSeq, heldSeq)
	}

	// Exactly one full-queue broadcast happened (the SetEnabled reset), proving the
	// hold and forward did NOT re-transmit the whole queue.
	updates := 0
	for _, m := range events() {
		if typeIs(m, "intercept.update") {
			updates++
		}
	}
	if updates != 1 {
		t.Fatalf("expected exactly one intercept.update (the enable reset), got %d — holds/forwards must be deltas", updates)
	}
}

// A dropped held request reports reason "dropped", distinct from a forward, so a
// mirror never confuses a drop for a forward.
func TestInterceptDropEmitsDroppedDelta(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	events, ready, stop := collectEvents(t, ts.URL)
	defer stop()
	ready()

	eng.SetEnabled(true)
	req, raw, flow := newHeldRequest(t, "/dropme")
	go eng.Hold(flow, req, raw)
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID
	waitEvent(t, events, "intercept.held", func(m map[string]any) bool { return typeIs(m, "intercept.held") })

	if err := eng.Drop(id); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	rel := waitEvent(t, events, "intercept.released(dropped)", func(m map[string]any) bool {
		return typeIs(m, "intercept.released") && m["reason"] == "dropped"
	})
	if int64(rel["id"].(float64)) != id {
		t.Fatalf("dropped id = %v, want %d", rel["id"], id)
	}
}

// A late joiner (a client that connects after a request is already held) must
// receive a full intercept.state snapshot as its first intercept frame, with the
// held item and a seq, so it is correct from the stream alone.
func TestNewSSEClientGetsInterceptStateSnapshot(t *testing.T) {
	h, _, eng := newHub(t)
	eng.SetEnabled(true)
	req, raw, flow := newHeldRequest(t, "/already-held")
	go eng.Hold(flow, req, raw)
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	events, ready, stop := collectEvents(t, ts.URL)
	defer stop()
	ready()

	state := waitEvent(t, events, "intercept.state", func(m map[string]any) bool { return typeIs(m, "intercept.state") })
	ic, _ := state["intercept"].(map[string]any)
	if ic == nil {
		t.Fatalf("intercept.state carried no snapshot: %+v", state)
	}
	if _, ok := ic["seq"].(float64); !ok {
		t.Fatalf("snapshot missing numeric seq: %+v", ic)
	}
	if en, _ := ic["enabled"].(bool); !en {
		t.Error("snapshot must report intercept enabled")
	}
	queue, _ := ic["queue"].([]any)
	if len(queue) != 1 {
		t.Fatalf("snapshot queue = %d items, want the one held request", len(queue))
	}
	first, _ := queue[0].(map[string]any)
	if first == nil || int64(first["id"].(float64)) != id {
		t.Fatalf("snapshot queue missing held id %d: %+v", id, queue)
	}
	if !strings.HasPrefix(req.URL.Path, "/already-held") {
		t.Fatal("sanity: wrong request path")
	}
}
