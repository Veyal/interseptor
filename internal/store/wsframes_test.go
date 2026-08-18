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

// SaveWSFrame rolls its insert back when retention fails, so an error cannot
// leave an unannounced frame behind or let the per-flow cap grow.
func TestSaveWSFramePropagatesPruneError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := wsFramesPerFlow
	wsFramesPerFlow = 1
	defer func() { wsFramesPerFlow = old }()

	if _, err := s.db.Exec(
		`CREATE TRIGGER ws_frames_no_delete BEFORE DELETE ON ws_frames
		 BEGIN SELECT RAISE(FAIL, 'prune blocked'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	// First frame: nothing to prune yet (count <= limit), so no error.
	if err := s.SaveWSFrame(&WSFrame{FlowID: 1, Dir: "send", Opcode: 1, Length: 1, Preview: "a"}); err != nil {
		t.Fatalf("first frame should not prune: %v", err)
	}
	// Second frame trips the retention DELETE, which the trigger fails.
	failed := &WSFrame{FlowID: 1, Dir: "send", Opcode: 1, Length: 1, Preview: "b"}
	if err := s.SaveWSFrame(failed); err == nil {
		t.Fatal("expected retention DELETE error to propagate, got nil")
	}
	if failed.ID != 0 {
		t.Fatalf("failed frame published id %d", failed.ID)
	}
	frames, err := s.QueryWSFrames(1, 10)
	if err != nil || len(frames) != 1 {
		t.Fatalf("frames after failed bounded insert = %d, err=%v; want 1", len(frames), err)
	}
}
