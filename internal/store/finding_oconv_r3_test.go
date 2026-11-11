package store

import (
	"errors"
	"fmt"
	"testing"
)

// The operator's editor sends the whole block list, and an agent over MCP can
// add evidence to the same finding while the operator types. The editor's next
// autosave then writes back the copy it rendered from, silently dropping the
// agent's block: in the browser an added "AGENT ADDED RESULT" block vanished
// after a 700 ms autosave. A save that names the version it started from must be
// refused when the finding has moved on.
func TestR2FindingUpdateRefusesAStaleBaseVersion(t *testing.T) {
	s := newTestStore(t)
	f := &Finding{Title: "Stale base", Severity: "High", Summary: "s"}
	id, err := s.CreateFinding(f)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	base := created.UpdatedTS
	if base == 0 {
		t.Fatal("a new finding has no updatedTs")
	}

	// An agent adds a block after the editor rendered.
	agentBody := `[{"type":"text","md":"AGENT ADDED RESULT"}]`
	body := agentBody
	if err := s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{}); err != nil {
		t.Fatal(err)
	}

	// The operator's autosave still names the version it rendered from.
	stale := `[{"type":"text","md":"operator step"}]`
	err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &stale, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		FindingMetadataPatch{IfUpdatedTS: base})
	if !errors.Is(err, ErrFindingChanged) {
		t.Fatalf("a stale save returned %v, want ErrFindingChanged", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != agentBody {
		t.Fatalf("the refused save still wrote: body = %q", got.Body)
	}

	// Re-reading and saving with the current version works.
	err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &stale, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		FindingMetadataPatch{IfUpdatedTS: got.UpdatedTS})
	if err != nil {
		t.Fatalf("a save from the current version was refused: %v", err)
	}
	// A caller that sends no base version keeps the old last-writer-wins
	// behaviour (MCP tools, imports and legacy clients rely on it).
	other := `[{"type":"text","md":"no base"}]`
	if err := s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &other, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{}); err != nil {
		t.Fatalf("an unconditional save was refused: %v", err)
	}
}

// The version is the guard's only signal, so two writes back to back must not
// share it: millisecond resolution can.
func TestR3FindingVersionStrictlyIncreasesPerWrite(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateFinding(&Finding{Title: "Version", Severity: "Low", Summary: "s"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	last := int64(0)
	for i := 0; i < 20; i++ {
		body := fmt.Sprintf(`[{"type":"text","md":"write %d"}]`, i)
		if err := s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		got, err := s.GetFinding(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.UpdatedTS <= last {
			t.Fatalf("write %d: updatedTs %d did not advance past %d", i, got.UpdatedTS, last)
		}
		if seen[got.UpdatedTS] {
			t.Fatalf("write %d reused version %d", i, got.UpdatedTS)
		}
		seen[got.UpdatedTS] = true
		last = got.UpdatedTS
	}
}
