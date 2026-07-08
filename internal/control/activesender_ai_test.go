package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/activescan"
	"github.com/Veyal/interseptor/internal/activescan/breaker"
	"github.com/Veyal/interseptor/internal/store"
)

// activeSender accepts extra flags OR'd onto each probe it records, so an
// AI-driven active scan (FlagActiveScan | FlagAI) can be surfaced in History.
func TestActiveSenderTagsAIFlag(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	send := h.activeSender(context.Background(), store.FlagAI, false, breaker.New())
	send(activescan.Target{Method: http.MethodGet, URL: target.URL + "/probe"})

	flows, err := s.QueryFlowsFilter(store.FlowFilter{RequireFlags: store.FlagActiveScan, Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("no active-scan flow recorded")
	}
	for _, f := range flows {
		if f.Flags&store.FlagAI == 0 {
			t.Fatalf("active-scan flow missing FlagAI, flags=%d", f.Flags)
		}
	}
}
