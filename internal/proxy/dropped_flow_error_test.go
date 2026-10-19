package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func waitHeld(t *testing.T, eng *intercept.Engine) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if q := eng.Queue(); len(q) > 0 {
			return q[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("request never entered the hold queue")
	return 0
}

// A held request that is dropped by the operator, or abandoned by its client,
// never gets a status. It must carry a terminal error so History does not show
// it as an in-flight ("pending") flow forever.
func TestDroppedHeldRequestRecordsTerminalError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(eng *intercept.Engine, id int64, cancel context.CancelFunc)
		want    string
	}{
		{"operator drop", func(eng *intercept.Engine, id int64, _ context.CancelFunc) { eng.Drop(id) }, errDroppedByUser},
		{"client canceled", func(_ *intercept.Engine, _ int64, cancel context.CancelFunc) { cancel() }, errClientCanceledHeld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := intercept.New()
			eng.SetEnabled(true)
			srv := &Server{eng: eng}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/held", nil).WithContext(ctx)
			flow := srv.newFlow(&store.Flow{Host: "example.com", Port: 80, Scheme: "http", Method: "GET", Path: "/held"})
			type res struct{ dropped bool }
			done := make(chan res, 1)
			go func() {
				_, dropped, _ := srv.gateAndForward(flow, r)
				done <- res{dropped}
			}()
			tc.release(eng, waitHeld(t, eng), cancel)
			got := <-done
			if !got.dropped {
				t.Fatal("gateAndForward did not report the request as dropped")
			}
			if flow.Flags&store.FlagDropped == 0 {
				t.Fatalf("flags = %d, want FlagDropped", flow.Flags)
			}
			if flow.Error != tc.want {
				t.Fatalf("flow.Error = %q, want %q", flow.Error, tc.want)
			}
		})
	}
}

func TestDroppedHeldResponseRecordsTerminalError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(eng *intercept.Engine, cancel context.CancelFunc)
		want    string
	}{
		{"operator drop", func(eng *intercept.Engine, _ context.CancelFunc) { eng.DropResponse(eng.ResponseQueue()[0].ID) }, errResponseDroppedByUser},
		{"client canceled", func(_ *intercept.Engine, cancel context.CancelFunc) { cancel() }, errClientCanceledHeld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := intercept.New()
			eng.SetResponseEnabled(true)
			srv := &Server{eng: eng}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       http.NoBody,
				Request:    httptest.NewRequest(http.MethodGet, "https://example.com/r", nil).WithContext(ctx),
			}
			flow := srv.newFlow(&store.Flow{Host: "example.com"})
			done := make(chan bool, 1)
			go func() {
				_, _, _, _, dropped := srv.maybeInterceptResponse(flow, resp)
				done <- dropped
			}()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && len(eng.ResponseQueue()) == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			tc.release(eng, cancel)
			if !<-done {
				t.Fatal("response was not dropped")
			}
			if flow.Error != tc.want {
				t.Fatalf("flow.Error = %q, want %q", flow.Error, tc.want)
			}
		})
	}
}
