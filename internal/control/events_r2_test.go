package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// openEventStream connects to /api/events and returns a channel of raw SSE lines.
func openEventStream(t *testing.T, ctx context.Context, base string) <-chan string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	lines := make(chan string, 4096)
	go func() {
		defer resp.Body.Close()
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return lines
}

func waitLine(t *testing.T, lines <-chan string, timeout time.Duration, what string, match func(string) bool) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %s", what)
			}
			if match(l) {
				return l
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func subscriberCount(h *Hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func waitSubscribers(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for subscriberCount(h) != want {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers = %d, want %d", subscriberCount(h), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// C4: an idle stream wrote nothing, so a proxy or tunnel in between closed it as
// idle and the client could not tell a dead stream from a quiet one.
func TestEventStreamHeartbeatOnIdleStream(t *testing.T) {
	orig := sseHeartbeatInterval
	sseHeartbeatInterval = 30 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = orig })

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := openEventStream(t, ctx, ts.URL)

	waitLine(t, lines, 2*time.Second, "hello", func(l string) bool { return l == "event: hello" })
	// No broadcast happens; the stream must still produce comment lines.
	for i := 0; i < 2; i++ {
		got := waitLine(t, lines, 2*time.Second, "a heartbeat comment", func(l string) bool { return strings.HasPrefix(l, ":") })
		if strings.HasPrefix(got, "data:") || strings.HasPrefix(got, "event:") {
			t.Fatalf("heartbeat must be an SSE comment, got %q", got)
		}
	}
}

// C2: a subscriber whose queue overflowed lost events with no signal. It must get
// one explicit resync marker (a `hello` the UI already answers with a full
// refetch) after the queue drains, and exactly one per overflow episode.
func TestEventStreamSlowSubscriberGetsResyncMarker(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := openEventStream(t, ctx, ts.URL)
	waitLine(t, lines, 2*time.Second, "hello", func(l string) bool { return l == "event: hello" })
	waitSubscribers(t, h, 1)

	// Overflow the 64-message queue: broadcast far faster than one handler can
	// write. Some of these are dropped.
	const burst = 5000
	for i := 0; i < burst; i++ {
		h.broadcast(map[string]any{"type": "pad", "n": i})
	}
	h.broadcast(map[string]any{"type": "sentinel"})

	// Read until the stream goes quiet, counting what arrived.
	delivered, hellos, resyncData := 0, 0, 0
	quiet := time.NewTimer(time.Second)
	defer quiet.Stop()
loop:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				break loop
			}
			switch {
			case strings.HasPrefix(l, `data: {"n":`):
				delivered++
			case l == "event: hello":
				hellos++
			case l == `data: {"resync":true}`:
				resyncData++
			}
			if !quiet.Stop() {
				<-quiet.C
			}
			quiet.Reset(500 * time.Millisecond)
		case <-quiet.C:
			break loop
		}
	}
	if delivered >= burst {
		t.Skipf("the queue never overflowed (%d/%d delivered); nothing to assert", delivered, burst)
	}
	if hellos == 0 || resyncData == 0 {
		t.Fatalf("%d of %d events were dropped for a slow subscriber and no resync marker followed (hello=%d resync=%d)", burst-delivered, burst, hellos, resyncData)
	}
	if hellos != resyncData {
		t.Fatalf("every mid-stream hello must carry resync:true (hello=%d resync=%d)", hellos, resyncData)
	}
	t.Logf("delivered %d/%d, resync markers %d", delivered, burst, hellos)
}

// A subscriber that keeps up never sees a resync marker.
func TestEventStreamNoResyncMarkerWithoutDrops(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := openEventStream(t, ctx, ts.URL)
	waitLine(t, lines, 2*time.Second, "hello", func(l string) bool { return l == "event: hello" })
	waitSubscribers(t, h, 1)

	for i := 0; i < 20; i++ {
		h.broadcast(map[string]any{"type": "pad", "n": i})
		waitLine(t, lines, 2*time.Second, "the event", func(l string) bool {
			if l == "event: hello" {
				t.Fatal("resync marker sent although nothing was dropped")
			}
			return strings.HasPrefix(l, fmt.Sprintf(`data: {"n":%d,`, i))
		})
	}
}

// Disconnecting ends the handler: the subscription (and its lag signal) is
// removed and no stream goroutine is left behind.
func TestEventStreamDisconnectLeavesNoGoroutine(t *testing.T) {
	orig := sseHeartbeatInterval
	sseHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = orig })

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		lines := openEventStream(t, ctx, ts.URL)
		waitLine(t, lines, 2*time.Second, "hello", func(l string) bool { return l == "event: hello" })
		waitSubscribers(t, h, 1)
		h.broadcast(map[string]any{"type": "pad"})
		cancel()
		waitSubscribers(t, h, 0)
		for range lines { // the reader goroutine ends with the body
		}
	}
	h.mu.Lock()
	lagLeft := len(h.clientLag)
	h.mu.Unlock()
	if lagLeft != 0 {
		t.Fatalf("%d lag signals left after every subscriber disconnected", lagLeft)
	}
	http.DefaultClient.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: %d before, %d after 10 connect/disconnect cycles", before, runtime.NumGoroutine())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func seedTagFlows(t *testing.T, st *store.Store, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		id, err := st.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https", Host: "app.example.com", Port: 443,
			Path: fmt.Sprintf("/p%d", i), HTTPVersion: "HTTP/1.1", Status: 200,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func subscribeRaw(h *Hub, size int) (chan string, func()) {
	ch := make(chan string, size)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() { h.mu.Lock(); delete(h.clients, ch); h.mu.Unlock() }
}

func postBulkTags(t *testing.T, h *Hub, ids []int64) time.Duration {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"flowIds": ids, "add": []string{"bulk"}})
	rec := httptest.NewRecorder()
	start := time.Now()
	(&flowAPI{h}).addFlowTagsBulk(rec, httptest.NewRequest(http.MethodPost, "/api/flows/tags", bytes.NewReader(body)))
	took := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk tag: status %d %s", rec.Code, rec.Body.String())
	}
	return took
}

func drainTypes(ch chan string) map[string]int {
	out := map[string]int{}
	for {
		select {
		case m := <-ch:
			var v struct {
				Type string         `json:"type"`
				Flow map[string]any `json:"flow"`
			}
			_ = json.Unmarshal([]byte(m), &v)
			key := v.Type
			if v.Type == "flow.update" && v.Flow == nil {
				key = "flow.update(reload)"
			}
			out[key]++
		default:
			return out
		}
	}
}

// C1: tagging a large selection sent one flow.update per flow (one GetFlow and
// one tag query each). 10,000 ids = 10,000 events into 64-slot subscriber queues.
func TestBulkTagLargeSelectionSendsOneCoalescedEvent(t *testing.T) {
	h, st, _ := newHub(t)
	ids := seedTagFlows(t, st, 10000)
	ch, unsub := subscribeRaw(h, 20000)
	defer unsub()

	took := postBulkTags(t, h, ids)
	got := drainTypes(ch)
	t.Logf("bulk tag of %d flows: handler %v, events %v", len(ids), took, got)
	if got["flow.update"] != 0 || got["flow.update(reload)"] != 1 {
		t.Fatalf("events for a 10,000-flow bulk tag: %v, want exactly one payload-less flow.update", got)
	}
	if got["tags.update"] != 1 {
		t.Fatalf("tags.update events: %d, want 1", got["tags.update"])
	}
	tags, err := st.FlowTags(ids[len(ids)-1])
	if err != nil || len(tags) != 1 || tags[0] != "bulk" {
		t.Fatalf("tags not stored: %v %v", tags, err)
	}
}

// A small selection still repaints each row in place, with its tags attached.
func TestBulkTagSmallSelectionKeepsRowEvents(t *testing.T) {
	h, st, _ := newHub(t)
	ids := seedTagFlows(t, st, 5)
	ch, unsub := subscribeRaw(h, 64)
	defer unsub()

	postBulkTags(t, h, ids)
	seen := map[int64]bool{}
	reloads := 0
	for len(ch) > 0 {
		var v struct {
			Type string `json:"type"`
			Flow *struct {
				ID   int64    `json:"id"`
				Tags []string `json:"tags"`
			} `json:"flow"`
		}
		_ = json.Unmarshal([]byte(<-ch), &v)
		if v.Type != "flow.update" {
			continue
		}
		if v.Flow == nil {
			reloads++
			continue
		}
		if len(v.Flow.Tags) != 1 || v.Flow.Tags[0] != "bulk" {
			t.Fatalf("flow %d update carries tags %v, want [bulk]", v.Flow.ID, v.Flow.Tags)
		}
		seen[v.Flow.ID] = true
	}
	if len(seen) != len(ids) || reloads != 0 {
		t.Fatalf("row updates for %d of %d flows, %d reload nudges", len(seen), len(ids), reloads)
	}
}
