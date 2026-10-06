package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestWSFrameNoteRoundTrip(t *testing.T) {
	s := newTestStore(t)
	for _, dir := range []string{"send", "recv"} {
		if err := s.SaveWSFrame(&WSFrame{FlowID: 5, Dir: dir, Opcode: 1, Length: 3, Preview: "abc"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FlushWSFrames(); err != nil {
		t.Fatal(err)
	}
	frames, _ := s.QueryWSFrames(5, 10)
	if len(frames) != 2 || frames[0].Note != "" {
		t.Fatalf("frames = %+v", frames)
	}

	if err := s.SetWSFrameNote(5, frames[1].ID, "server rejects invalid token"); err != nil {
		t.Fatalf("SetWSFrameNote: %v", err)
	}
	frames, _ = s.QueryWSFrames(5, 10)
	if frames[1].Note != "server rejects invalid token" || frames[0].Note != "" {
		t.Fatalf("notes = %q / %q", frames[0].Note, frames[1].Note)
	}

	// A frame id belonging to another flow must not be annotatable via this flow.
	if err := s.SetWSFrameNote(6, frames[1].ID, "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-flow note err = %v, want sql.ErrNoRows", err)
	}
	if err := s.SetWSFrameNote(5, frames[1].ID, strings.Repeat("n", MaxWSFrameNoteBytes+1)); err == nil {
		t.Fatal("expected oversize note rejection")
	}
	if err := s.SetWSFrameNote(5, frames[1].ID, ""); err != nil {
		t.Fatalf("clear note: %v", err)
	}
}
