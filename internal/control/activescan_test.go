package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interceptor/internal/activescan"
	"github.com/Veyal/interceptor/internal/store"
)

// Every active-scan probe is logged and persisted as a FlagActiveScan flow, even
// when no vulnerability is confirmed.
func TestActiveScanProbeLogAndHistory(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	send := h.activeSender(context.Background(), 0)
	send(activescan.Target{Method: http.MethodGet, URL: target.URL + "/a?q=1"})
	send(activescan.Target{Method: http.MethodGet, URL: target.URL + "/b?q=2"})

	h.as.mu.Lock()
	if len(h.as.logs) != 2 {
		t.Fatalf("expected 2 probe logs, got %d", len(h.as.logs))
	}
	for _, l := range h.as.logs {
		if l.FlowID == 0 || l.Status != http.StatusOK {
			t.Fatalf("unexpected log entry: %+v", l)
		}
	}
	h.as.mu.Unlock()

	flows, err := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagActiveScan, Limit: 10})
	if err != nil {
		t.Fatalf("query flows: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("expected 2 active-scan flows in store, got %d", len(flows))
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/activescan/history")
	if err != nil {
		t.Fatalf("GET history: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			ID int64 `json:"id"`
		} `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Flows) != 2 {
		t.Fatalf("history returned %d flows, want 2", len(out.Flows))
	}
}

func TestActiveScanLogsTransportError(t *testing.T) {
	h, s, _ := newHub(t)
	send := h.activeSender(context.Background(), 0)
	send(activescan.Target{Method: http.MethodGet, URL: "http://127.0.0.1:1/nope"})

	h.as.mu.Lock()
	if len(h.as.logs) != 1 {
		t.Fatalf("expected 1 probe log, got %d", len(h.as.logs))
	}
	if h.as.logs[0].FlowID == 0 {
		t.Fatal("transport failure should still record a flow id")
	}
	if h.as.logs[0].Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", h.as.logs[0].Status)
	}
	h.as.mu.Unlock()

	flows, _ := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagActiveScan, Limit: 5})
	if len(flows) != 1 {
		t.Fatalf("expected 1 errored flow in store, got %d", len(flows))
	}
}
