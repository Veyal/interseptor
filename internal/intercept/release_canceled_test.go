package intercept

import (
	"context"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A held item released because its client went away is not an operator drop:
// the UI names it (toast) instead of silently replacing the open editor.
func TestClientCancelReportsCanceledRelease(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	var log changeLog
	e.SetChangeNotifier(log.record())

	ctx, cancel := context.WithCancel(context.Background())
	go e.HoldContext(ctx, &store.Flow{}, newReq(t, "GET", "https://example.com/p", ""), nil)
	waitQueue(t, e, 1)
	id := e.Queue()[0].ID
	cancel()
	if rel := log.waitFor(t, ChangeReleased, id); rel.Reason != ReleaseCanceled || rel.Side != SideRequest {
		t.Fatalf("request cancel change = %+v, want reason %q", rel, ReleaseCanceled)
	}

	// A fresh engine: response and request ids are counted separately.
	e = New()
	e.SetResponseEnabled(true)
	log = changeLog{}
	e.SetChangeNotifier(log.record())
	rctx, rcancel := context.WithCancel(context.Background())
	go e.HoldResponseContext(rctx, &store.Flow{}, []byte("HTTP/1.1 200 OK\r\n\r\n"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	rid := e.ResponseQueue()[0].ID
	rcancel()
	if rel := log.waitFor(t, ChangeReleased, rid); rel.Reason != ReleaseCanceled || rel.Side != SideResponse {
		t.Fatalf("response cancel change = %+v, want reason %q", rel, ReleaseCanceled)
	}
}
