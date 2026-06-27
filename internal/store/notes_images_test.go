package store

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
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

func strconvParseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
