package store

import (
	"testing"
	"time"
)

// wsFrameCount returns how many ws_frames rows reference flowID.
func wsFrameCount(t *testing.T, s *Store, flowID int64) int {
	t.Helper()
	if err := s.FlushWSFrames(); err != nil {
		t.Fatalf("FlushWSFrames: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ws_frames WHERE flow_id=?`, flowID).Scan(&n); err != nil {
		t.Fatalf("count ws_frames: %v", err)
	}
	return n
}

func addFrame(t *testing.T, s *Store, flowID int64) {
	t.Helper()
	if err := s.SaveWSFrame(&WSFrame{FlowID: flowID, Dir: "send", Opcode: 1, Length: 1, Preview: "x"}); err != nil {
		t.Fatalf("SaveWSFrame: %v", err)
	}
}

// Deleting a flow must delete its WebSocket frames; otherwise a reused flow id
// (nextFlowID restarts at MAX(id) after the highest flow is deleted) would
// show the deleted flow's frames.
func TestDeleteFlowsRemovesWSFrames(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	keep := mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	gone := mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	addFrame(t, s, keep)
	addFrame(t, s, gone)
	if _, err := s.DeleteFlows([]int64{gone}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}
	if n := wsFrameCount(t, s, gone); n != 0 {
		t.Fatalf("deleted flow kept %d ws frames", n)
	}
	if n := wsFrameCount(t, s, keep); n != 1 {
		t.Fatalf("kept flow has %d ws frames, want 1", n)
	}
	s.Close()

	// Restart: the highest id is reused and must start with no frames.
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	reused := mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	got, err := s.QueryWSFrames(reused, 100)
	if err != nil {
		t.Fatalf("QueryWSFrames: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("new flow %d (deleted %d) inherited %d ws frames", reused, gone, len(got))
	}
}

func TestRetentionDeletesRemoveWSFrames(t *testing.T) {
	cases := map[string]func(s *Store) error{
		"byHost": func(s *Store) error {
			_, err := s.DeleteFlowsByHost([]string{"old.example.com"}, false)
			return err
		},
		"olderThan": func(s *Store) error {
			_, err := s.DeleteFlowsOlderThan(5)
			return err
		},
		"keepNewest": func(s *Store) error {
			_, err := s.DeleteFlowsKeepNewest(1)
			return err
		},
	}
	for name, del := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			gone := mustInsertFlow(t, s, "old.example.com", "", "", 0, 0) // ts=1ms
			keep, err := s.InsertFlow(&Flow{TS: time.UnixMilli(10), Method: "GET", Host: "example.com", Path: "/"})
			if err != nil {
				t.Fatalf("InsertFlow: %v", err)
			}
			addFrame(t, s, gone)
			addFrame(t, s, keep)
			if err := del(s); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if n := wsFrameCount(t, s, gone); n != 0 {
				t.Fatalf("deleted flow kept %d ws frames", n)
			}
			if n := wsFrameCount(t, s, keep); n != 1 {
				t.Fatalf("kept flow has %d ws frames, want 1", n)
			}
		})
	}
}
