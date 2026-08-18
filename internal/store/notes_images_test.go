package store

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Dangerous MIME types must never survive insert or serve; only the raster
// allowlist is kept, everything else is coerced inert so a stored notebook
// image can't run as active content (stored-XSS prevention).
func TestNotesImageMIMESanitized(t *testing.T) {
	cases := map[string]string{
		"image/png":                 "image/png",
		"image/JPEG":                "image/jpeg",
		"image/webp; charset=utf-8": "image/webp",
		"text/html":                 "application/octet-stream",
		"image/svg+xml":             "application/octet-stream",
		"application/javascript":    "application/octet-stream",
		"":                          "application/octet-stream",
	}
	for in, want := range cases {
		if got := SanitizeNotesImageMIME(in); got != want {
			t.Fatalf("SanitizeNotesImageMIME(%q) = %q, want %q", in, got, want)
		}
	}

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	id, err := s.InsertNotesImage("text/html", []byte("<script>alert(1)</script>"))
	if err != nil {
		t.Fatalf("InsertNotesImage: %v", err)
	}
	mime, _, err := s.GetNotesImage(id)
	if err != nil {
		t.Fatalf("GetNotesImage: %v", err)
	}
	if mime != "application/octet-stream" {
		t.Fatalf("stored mime = %q, want application/octet-stream", mime)
	}
}

func TestNormalizeNotesMarkdownStoresDataURL(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	png := []byte{0x89, 0x50, 0x4e, 0x47}
	b64 := base64.StdEncoding.EncodeToString(png)
	in := "shot\n\n![pasted](data:image/png;base64," + b64 + ")\n"
	out, err := s.NormalizeNotesMarkdown(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "data:image/") {
		t.Fatalf("expected data URL replaced, got %q", out)
	}
	m := notesImgRefRE.FindStringSubmatch(out)
	if len(m) != 2 {
		t.Fatalf("expected /api/notes/images/{id}, got %q", out)
	}
	id := strconvParseInt(m[1])
	mime, data, err := s.GetNotesImage(id)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || string(data) != string(png) {
		t.Fatalf("stored image = %q %d bytes, want png %d bytes", mime, len(data), len(png))
	}
}

func TestGCNotesImagesDropsOrphans(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	keep, err := s.InsertNotesImage("image/png", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.InsertNotesImage("image/png", []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	notes := "![x](/api/notes/images/" + itoa(keep) + ")"
	if err := s.GCNotesImages(notes); err != nil {
		t.Fatal(err)
	}
	ok, _ := s.NotesImageExists(keep)
	if !ok {
		t.Fatal("referenced image was deleted")
	}
	ok, _ = s.NotesImageExists(orphan)
	if ok {
		t.Fatal("orphan image should be deleted")
	}
}

func TestPersistNotesRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	b64 := base64.StdEncoding.EncodeToString([]byte("img"))
	in := "![pasted](data:image/gif;base64," + b64 + ")"
	out, err := s.PersistNotes(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadNotes()
	if err != nil {
		t.Fatal(err)
	}
	if got != out || strings.Contains(got, "data:image/") {
		t.Fatalf("persisted notes = %q", got)
	}
}

func TestPersistNotesPreservesRecentUploadUntilMarkdownReferencesIt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.InsertNotesImage("image/png", []byte("pending upload"))
	if err != nil {
		t.Fatalf("InsertNotesImage: %v", err)
	}
	if _, err := s.PersistNotes("unrelated autosave"); err != nil {
		t.Fatalf("PersistNotes: %v", err)
	}
	if exists, err := s.NotesImageExists(id); err != nil || !exists {
		t.Fatalf("recent upload was collected before attachment: exists=%v err=%v", exists, err)
	}

	if _, err := s.db.Exec(`UPDATE notes_images SET ts=? WHERE id=?`, time.Now().Add(-notesImageUploadGrace-time.Second).UnixMilli(), id); err != nil {
		t.Fatalf("age image: %v", err)
	}
	if _, err := s.PersistNotes("later autosave"); err != nil {
		t.Fatalf("PersistNotes after grace: %v", err)
	}
	if exists, err := s.NotesImageExists(id); err != nil || exists {
		t.Fatalf("expired orphan survived: exists=%v err=%v", exists, err)
	}
}

func TestPersistNotesRollsBackImagesWhenSettingWriteFails(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	oldID, err := s.InsertNotesImage("image/png", []byte("old image"))
	if err != nil {
		t.Fatalf("InsertNotesImage: %v", err)
	}
	oldNotes := "![old](/api/notes/images/" + itoa(oldID) + ")"
	if err := s.SetSetting("project.notes", oldNotes); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_project_notes BEFORE INSERT ON settings
		WHEN NEW.key = 'project.notes'
		BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	inline := "![new](data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("new image")) + ")"
	if _, err := s.PersistNotes(inline); err == nil {
		t.Fatal("PersistNotes succeeded despite rejected setting write")
	}
	got, _, err := s.GetSetting("project.notes")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != oldNotes {
		t.Fatalf("notes = %q, want old notes", got)
	}
	if exists, err := s.NotesImageExists(oldID); err != nil || !exists {
		t.Fatalf("old referenced image was lost: exists=%v err=%v", exists, err)
	}
	if exists, err := s.NotesImageExists(oldID + 1); err != nil || exists {
		t.Fatalf("new image survived failed notes write: exists=%v err=%v", exists, err)
	}
}

func TestPersistNotesSharesAppendLock(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.notesMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.PersistNotes("replacement")
		done <- err
	}()
	select {
	case err := <-done:
		s.notesMu.Unlock()
		t.Fatalf("PersistNotes bypassed append lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	s.notesMu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("PersistNotes after unlock: %v", err)
	}
}

// AppendNote must be atomic: N concurrent appends must all survive, even when
// they race against each other, because it does its own read-modify-write
// inside the store rather than relying on a client-side GET-then-PUT (which is
// what the old append_notes MCP tool did, and which loses entries under race).
func TestAppendNoteConcurrentNoLoss(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			if err := s.AppendNote(fmt.Sprintf("entry-%d", i)); err != nil {
				t.Errorf("AppendNote(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := s.LoadNotes()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("entry-%d", i)
		if !strings.Contains(got, want) {
			t.Fatalf("lost update: missing %q in final notes; got:\n%s", want, got)
		}
	}
}

// AppendNote on an empty notebook should not leave a leading separator.
func TestAppendNoteFirstEntryNoLeadingSeparator(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.AppendNote("first"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadNotes()
	if err != nil {
		t.Fatal(err)
	}
	if got != "first" {
		t.Fatalf("notes = %q, want %q", got, "first")
	}
}

func strconvParseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
