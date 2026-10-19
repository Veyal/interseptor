package intercept

import (
	"errors"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A drop the operator scheduled (the UI's 5 s Undo window) must win over every
// bulk release: turning intercept off drops the marked item instead of
// forwarding it, and nobody can Forward it until the mark is cleared.
func TestDropPendingRequestIsDroppedWhenInterceptTurnsOff(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	dropped := make(chan Decision, 1)
	kept := make(chan Decision, 1)
	go func() { dropped <- e.Hold(&store.Flow{}, newReq(t, "GET", "https://example.com/drop-me", ""), nil) }()
	waitQueue(t, e, 1)
	go func() { kept <- e.Hold(&store.Flow{}, newReq(t, "GET", "https://example.com/keep", ""), nil) }()
	waitQueue(t, e, 2)
	id := e.Queue()[0].ID

	if err := e.SetDropPending(id, true); err != nil {
		t.Fatalf("SetDropPending: %v", err)
	}
	if !e.Snapshot().Queue[0].DropPending {
		t.Fatal("snapshot must report the drop-pending mark")
	}
	if err := e.Forward(id, nil); !errors.Is(err, ErrDropPending) {
		t.Fatalf("Forward of a drop-pending item = %v, want ErrDropPending", err)
	}
	e.SetEnabled(false)
	if d := recvDecision(t, dropped); !d.Drop || !d.Held {
		t.Fatalf("drop-pending item on intercept off = %+v, want a held drop", d)
	}
	if d := recvDecision(t, kept); d.Drop || d.Request == nil {
		t.Fatalf("unmarked item on intercept off = %+v, want forward", d)
	}
}

// Undo clears the mark: the item can be forwarded again and is forwarded by a
// later intercept-off drain.
func TestDropPendingUndoRestoresForward(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	got := make(chan Decision, 1)
	go func() { got <- e.Hold(&store.Flow{}, newReq(t, "GET", "https://example.com/p", ""), nil) }()
	waitQueue(t, e, 1)
	id := e.Queue()[0].ID
	if err := e.SetDropPending(id, true); err != nil {
		t.Fatal(err)
	}
	if err := e.SetDropPending(id, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Forward(id, nil); err != nil {
		t.Fatalf("Forward after undo: %v", err)
	}
	if d := recvDecision(t, got); d.Drop {
		t.Fatalf("decision = %+v, want forward", d)
	}
	if err := e.SetDropPending(id, true); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("mark of a released item = %v, want ErrNotHeld", err)
	}
}

func TestDropPendingResponseIsDroppedWhenResponseInterceptTurnsOff(t *testing.T) {
	e := New()
	e.SetResponseEnabled(true)
	got := make(chan ResponseDecision, 1)
	go func() { got <- e.HoldResponse(&store.Flow{}, []byte("HTTP/1.1 200 OK\r\n\r\n")) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(e.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	id := e.ResponseQueue()[0].ID
	if err := e.SetResponseDropPending(id, true); err != nil {
		t.Fatalf("SetResponseDropPending: %v", err)
	}
	if err := e.ForwardResponse(id, nil); !errors.Is(err, ErrDropPending) {
		t.Fatalf("ForwardResponse of a drop-pending item = %v, want ErrDropPending", err)
	}
	e.SetResponseEnabled(false)
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatalf("drop-pending response on intercept off = %+v, want drop", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response never released")
	}
}
