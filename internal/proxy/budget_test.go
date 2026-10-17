package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func budgetTestServer(t *testing.T, budget int64) *Server {
	t.Helper()
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x", BigBody: true}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	return &Server{eng: eng, budget: newByteBudget(budget)}
}

func budgetTestResponse(body []byte, knownLength bool) *http.Response {
	cl := int64(-1)
	if knownLength {
		cl = int64(len(body))
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"application/octet-stream"}},
		ContentLength: cl,
		Body:          io.NopCloser(bytes.NewReader(body)),
		Request:       httptest.NewRequest(http.MethodGet, "http://example.com/big", nil),
	}
}

func TestResponseTransformBudget(t *testing.T) {
	const budget = 1 << 20
	for _, known := range []bool{true, false} {
		name := "unknown length"
		if known {
			name = "known length"
		}
		t.Run(name+" within budget is transformed and released", func(t *testing.T) {
			srv := budgetTestServer(t, budget)
			body := bytes.Repeat([]byte("a"), budget/2)
			flow := srv.newFlow(&store.Flow{Host: "example.com"})
			_, _, got, transformed, _ := srv.maybeInterceptResponse(flow, budgetTestResponse(body, known))
			if !transformed || !bytes.Equal(got, body) {
				t.Fatalf("transformed=%v len=%d, want transformed copy of %d bytes", transformed, len(got), len(body))
			}
			if srv.budget.used.Load() == 0 {
				t.Fatal("buffered body was not charged to the budget")
			}
			srv.releaseBuffered(flow)
			if used := srv.budget.used.Load(); used != 0 {
				t.Fatalf("budget used after release = %d, want 0", used)
			}
		})
		t.Run(name+" over budget streams untransformed", func(t *testing.T) {
			srv := budgetTestServer(t, budget)
			body := bytes.Repeat([]byte("b"), 2*budget)
			resp := budgetTestResponse(body, known)
			flow := srv.newFlow(&store.Flow{Host: "example.com"})
			_, _, _, transformed, dropped := srv.maybeInterceptResponse(flow, resp)
			if transformed || dropped {
				t.Fatalf("over-budget response transformed=%v dropped=%v, want streamed", transformed, dropped)
			}
			if used := srv.budget.used.Load(); used != 0 {
				t.Fatalf("budget used after overflow = %d, want 0", used)
			}
			rest, err := io.ReadAll(resp.Body)
			if err != nil || !bytes.Equal(rest, body) {
				t.Fatalf("restored body len=%d err=%v, want the full %d bytes", len(rest), err, len(body))
			}
		})
	}
}

// Concurrent responses share one budget: once it is spent, further responses
// stream untransformed instead of growing memory without bound.
func TestResponseTransformBudgetIsShared(t *testing.T) {
	const budget = 1 << 20
	srv := budgetTestServer(t, budget)
	body := bytes.Repeat([]byte("c"), 400<<10) // two fit, the third does not
	var flows []*proxyFlow
	transformedCount := 0
	for i := 0; i < 3; i++ {
		flow := srv.newFlow(&store.Flow{Host: "example.com"})
		flows = append(flows, flow)
		if _, _, _, tr, _ := srv.maybeInterceptResponse(flow, budgetTestResponse(body, true)); tr {
			transformedCount++
		}
	}
	if transformedCount != 2 {
		t.Fatalf("transformed %d of 3 responses with room for 2", transformedCount)
	}
	for _, f := range flows {
		srv.releaseBuffered(f)
	}
	if used := srv.budget.used.Load(); used != 0 {
		t.Fatalf("budget used after releases = %d, want 0", used)
	}
}

func TestRequestHoldBufferRespectsBudget(t *testing.T) {
	b := newByteBudget(1 << 10)
	body := strings.Repeat("d", 4<<10)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/upload", strings.NewReader(body))
	raw, truncated, held := dumpRequestWithin(req, b)
	if !truncated || held != 0 {
		t.Fatalf("over-budget request truncated=%v held=%d, want hold bypassed", truncated, held)
	}
	if bytes.Contains(raw, []byte(body)) {
		t.Fatal("over-budget request body was rendered for editing")
	}
	got, _ := io.ReadAll(req.Body)
	if string(got) != body {
		t.Fatalf("restored request body len=%d, want %d", len(got), len(body))
	}
	if used := b.used.Load(); used != 0 {
		t.Fatalf("budget used = %d, want 0", used)
	}
}

func TestByteBudgetConcurrentAcquireNeverOvershoots(t *testing.T) {
	b := newByteBudget(1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := int64(0)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.tryAcquire(100) {
				mu.Lock()
				granted += 100
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 1000 || b.used.Load() != 1000 {
		t.Fatalf("granted=%d used=%d, want exactly the 1000-byte budget", granted, b.used.Load())
	}
}
