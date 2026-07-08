package proxy

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// panicEvents implements Events plus a WSFramed notifier that always panics,
// simulating a store/SSE callback blowing up on the frame-record path.
type panicEvents struct{}

func (panicEvents) FlowCaptured(*store.Flow) {}
func (panicEvents) FlowUpdated(*store.Flow)  {}
func (panicEvents) WSFramed(int64)           { panic("boom in notifier") }

// TestRelayWSSurvivesNotifierPanic asserts that a panic in the frame-record
// notifier path does not hang tunnelUpgrade's <-done wait and does not crash
// the process: relayWS recovers, closes the conn, and signals done.
func TestRelayWSSurvivesNotifierPanic(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, nil, nil, nil, panicEvents{})

	// src carries one WS frame then EOF; dst is where the relay forwards.
	srcR, srcW := net.Pipe()
	dstR, dstW := net.Pipe()
	defer dstR.Close()

	// Drain the forwarded bytes so the relay's dst.Write never blocks.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := dstR.Read(buf); err != nil {
				return
			}
		}
	}()

	go func() {
		srcW.Write(wsTextFrame("hello", false))
		srcW.Close() // EOF ends the relay loop after the frame is recorded
	}()

	done := make(chan struct{}, 1)
	go s.relayWS(1, "recv", bufio.NewReader(srcR), dstW, dstW, done)

	select {
	case <-done:
		// relay returned cleanly despite the notifier panic — success.
	case <-time.After(2 * time.Second):
		t.Fatal("relayWS hung after notifier panic (goroutine leak / deadlock)")
	}
}
