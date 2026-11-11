package intruder

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// Close is what shutdown waits on before the store is closed. The run record
// is written by the sink, so Close must not return while the sink is still
// running: otherwise the last run's record races the store closing and is lost.
func TestCloseWaitsForRunSink(t *testing.T) {
	up := okServer(t, 0)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := New(sender.New(s, capture.New(s)))

	var sinkDone atomic.Bool
	entered := make(chan struct{})
	e.SetRunSink(func(RunRecord) {
		close(entered)
		time.Sleep(300 * time.Millisecond) // a slow store write
		sinkDone.Store(true)
	})
	if err := e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("sink never ran")
	}
	e.Close()
	if !sinkDone.Load() {
		t.Fatal("Close returned while the run sink was still persisting the record")
	}
}
