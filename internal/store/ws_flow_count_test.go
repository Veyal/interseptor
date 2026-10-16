package store

import (
	"testing"
	"time"
)

func TestWSFlowCountCountsDistinctFlowsWithFrames(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if n, err := s.WSFlowCount(); err != nil || n != 0 {
		t.Fatalf("empty store: n=%d err=%v, want 0", n, err)
	}
	for _, f := range []WSFrame{
		{FlowID: 7, TS: time.Now(), Dir: "out", Opcode: 1, Length: 3, Preview: "a"},
		{FlowID: 7, TS: time.Now(), Dir: "in", Opcode: 1, Length: 3, Preview: "b"},
		{FlowID: 9, TS: time.Now(), Dir: "in", Opcode: 1, Length: 3, Preview: "c"},
	} {
		f := f
		if err := s.SaveWSFrame(&f); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FlushWSFrames(); err != nil {
		t.Fatal(err)
	}
	if n, err := s.WSFlowCount(); err != nil || n != 2 {
		t.Fatalf("WSFlowCount = %d, %v; want 2 distinct flows", n, err)
	}
}
