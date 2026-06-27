package store

import "testing"

// SaveWSFrame trims a flow to the most recent wsFramesPerFlow frames so a long
// WebSocket can't grow the table without bound; other flows are untouched.
func TestSaveWSFrameCapsPerFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := wsFramesPerFlow
	wsFramesPerFlow = 10
	defer func() { wsFramesPerFlow = old }()

	for i := 0; i < 25; i++ {
		if err := s.SaveWSFrame(&WSFrame{FlowID: 1, Dir: "send", Opcode: 1, Length: 1, Preview: "x"}); err != nil {
			t.Fatalf("SaveWSFrame: %v", err)
		}
	}
	got, _ := s.QueryWSFrames(1, 1000)
	if len(got) != 10 {
		t.Fatalf("flow 1 should be capped at 10, got %d", len(got))
	}

	s.SaveWSFrame(&WSFrame{FlowID: 2, Dir: "recv", Opcode: 1, Length: 1, Preview: "y"})
	if g2, _ := s.QueryWSFrames(2, 1000); len(g2) != 1 {
		t.Fatalf("flow 2 should be unaffected (1 frame), got %d", len(g2))
	}
}
