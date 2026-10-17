package store

import (
	"errors"
	"testing"
)

// A conditional notebook save only lands when the caller saw the current
// revision, so a UI autosave cannot silently overwrite an agent's append.
func TestPersistNotesIfRevRejectsStaleBase(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.PersistNotes("# base"); err != nil {
		t.Fatal(err)
	}
	base := NotesRev("# base")
	if err := s.AppendNote("agent line"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PersistNotesIfRev(base, "# base\nlocal"); !errors.Is(err, ErrNotesConflict) {
		t.Fatalf("stale save err = %v, want ErrNotesConflict", err)
	}
	got, _ := s.LoadNotes()
	if got != "# base\n\nagent line" {
		t.Fatalf("notes after rejected save = %q", got)
	}

	saved, err := s.PersistNotesIfRev(NotesRev(got), got+"\nlocal")
	if err != nil {
		t.Fatalf("fresh save: %v", err)
	}
	if saved != got+"\nlocal" {
		t.Fatalf("saved = %q", saved)
	}
	if NotesRev("") == NotesRev("x") || NotesRev("x") != NotesRev("x") {
		t.Fatal("NotesRev must be deterministic and content-sensitive")
	}
}
