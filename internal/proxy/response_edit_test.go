package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// Editing a held response must keep the origin's head as the original, so
// History can show what the server really sent next to the edit.
func TestHeldResponseEditRecordsOriginalHeaders(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	eng := intercept.New()
	eng.SetResponseEnabled(true)
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.budget = newByteBudget(1 << 20)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if q := eng.ResponseQueue(); len(q) > 0 {
				_ = eng.ForwardResponse(q[0].ID, []byte("HTTP/1.1 200 OK\r\nX-Origin: edited\r\n\r\nedited body"))
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"X-Origin": {"server"}},
		ContentLength: 11,
		Body:          io.NopCloser(strings.NewReader("server body")),
		Request:       httptest.NewRequest(http.MethodGet, "http://example.com/", nil),
	}
	flow := srv.newFlow(&store.Flow{Host: "example.com"})
	_, h, body, transformed, _ := srv.maybeInterceptResponse(flow, resp)
	defer srv.releaseBuffered(flow)
	if !transformed || string(body) != "edited body" || h.Get("X-Origin") != "edited" {
		t.Fatalf("edit not applied: transformed=%v body=%q X-Origin=%q", transformed, body, h.Get("X-Origin"))
	}
	if got := http.Header(flow.OriginalResHeaders).Get("X-Origin"); got != "server" {
		t.Fatalf("original X-Origin = %q, want the origin's head (server)", got)
	}
	if flow.OriginalResBodyHash == "" {
		t.Fatal("original response body was not kept for an edited hold")
	}
	rc, err := st.OpenBody(flow.OriginalResBodyHash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "server body" {
		t.Fatalf("original body = %q, want the origin's body", got)
	}
}
