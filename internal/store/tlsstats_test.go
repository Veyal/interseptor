package store

import (
	"testing"
	"time"
)

// The TLS diagnostic report answers "is interception working?" from flow counts.
// Capture is write-behind, so a flow the proxy already handed to the store can
// still be queued when the operator opens the report; counting only committed
// rows would tell them nothing was intercepted.
func TestTLSCountsDrainQueuedCapture(t *testing.T) {
	flowApplyDelayNS.Store(int64(500 * time.Millisecond))
	defer flowApplyDelayNS.Store(0)

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if ok, err := s.EnqueueInsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "counted.example.test", Path: "/t", Status: 200}); !ok || err != nil {
		t.Fatalf("EnqueueInsertFlow: ok=%v err=%v", ok, err)
	}
	if ok, err := s.EnqueueInsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "failed.example.test", Path: "/t", Status: 200, Flags: FlagTLSFailed}); !ok || err != nil {
		t.Fatalf("EnqueueInsertFlow: ok=%v err=%v", ok, err)
	}

	ok, err := s.CountSuccessfulHTTPS("")
	if err != nil {
		t.Fatalf("CountSuccessfulHTTPS: %v", err)
	}
	if ok != 1 {
		t.Fatalf("CountSuccessfulHTTPS = %d, want the queued successful flow counted", ok)
	}
	failed, err := s.CountFlowsWithFlag(FlagTLSFailed)
	if err != nil {
		t.Fatalf("CountFlowsWithFlag: %v", err)
	}
	if failed != 1 {
		t.Fatalf("CountFlowsWithFlag = %d, want the queued failed flow counted", failed)
	}
}
