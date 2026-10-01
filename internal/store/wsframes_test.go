package store

import (
	"strconv"
	"testing"
)

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
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("FlushWSFrames: %v", err)
	}
	got, _ := s.QueryWSFrames(1, 1000)
	if len(got) != 10 {
		t.Fatalf("flow 1 should be capped at 10, got %d", len(got))
	}

	if err := s.SaveWSFrame(&WSFrame{FlowID: 2, Dir: "recv", Opcode: 1, Length: 1, Preview: "y"}); err != nil {
		t.Fatalf("SaveWSFrame flow 2: %v", err)
	}
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("FlushWSFrames flow 2: %v", err)
	}
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
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("first flush should not prune: %v", err)
	}
	// Second frame trips the retention DELETE, which the trigger fails.
	// The insert and the trim share one transaction, so the failure rolls
	// the new frame back.
	failed := &WSFrame{FlowID: 1, Dir: "send", Opcode: 1, Length: 1, Preview: "b"}
	if err := s.SaveWSFrame(failed); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := s.FlushWSFrames(); err == nil {
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

func TestSaveWSFrameBatchPreservesOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const n = 80
	for i := 0; i < n; i++ {
		preview := string(rune('a'+(i%26))) + string(rune('0'+i/26))
		if err := s.SaveWSFrame(&WSFrame{FlowID: 7, Dir: "send", Opcode: 1, Length: 1, Preview: preview}); err != nil {
			t.Fatalf("SaveWSFrame %d: %v", i, err)
		}
	}
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("FlushWSFrames: %v", err)
	}
	got, err := s.QueryWSFrames(7, n)
	if err != nil {
		t.Fatalf("QueryWSFrames: %v", err)
	}
	if len(got) != n {
		t.Fatalf("persisted %d frames, want %d", len(got), n)
	}
	for i, fr := range got {
		preview := string(rune('a'+(i%26))) + string(rune('0'+i/26))
		if fr.Preview != preview {
			t.Fatalf("frame %d preview %q, want %q", i, fr.Preview, preview)
		}
	}
}

func TestSaveWSFrameDropsOldestWhenBufferFull(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	s.wsMu.Lock()
	s.wsPaused = true
	s.wsMu.Unlock()

	const extra = 5
	for i := 0; i < wsFrameBufCap+extra; i++ {
		if err := s.SaveWSFrame(&WSFrame{FlowID: 1, Dir: "send", Opcode: 1, Length: 1, Preview: strconv.Itoa(i)}); err != nil {
			t.Fatalf("SaveWSFrame %d: %v", i, err)
		}
	}
	if got := s.wsDropped.Load(); got != extra {
		t.Fatalf("dropped %d, want %d", got, extra)
	}
	s.wsMu.Lock()
	s.wsPaused = false
	s.wsMu.Unlock()
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("FlushWSFrames: %v", err)
	}
	got, err := s.QueryWSFrames(1, wsFrameBufCap+extra)
	if err != nil {
		t.Fatalf("QueryWSFrames: %v", err)
	}
	if len(got) != wsFrameBufCap {
		t.Fatalf("persisted %d, want %d", len(got), wsFrameBufCap)
	}
	if got[0].Preview != strconv.Itoa(extra) || got[len(got)-1].Preview != strconv.Itoa(wsFrameBufCap+extra-1) {
		t.Fatalf("kept previews %q..%q, want %q..%q", got[0].Preview, got[len(got)-1].Preview, strconv.Itoa(extra), strconv.Itoa(wsFrameBufCap+extra-1))
	}
}

func TestCloseDrainsPendingWSFrames(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.SaveWSFrame(&WSFrame{FlowID: 1, Dir: "recv", Opcode: 1, Length: 1, Preview: strconv.Itoa(i)}); err != nil {
			t.Fatalf("SaveWSFrame: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.QueryWSFrames(1, 10)
	if err != nil {
		t.Fatalf("QueryWSFrames: %v", err)
	}
	if len(got) != 3 || got[0].Preview != "0" || got[2].Preview != "2" {
		t.Fatalf("drained frames = %+v", got)
	}
}
