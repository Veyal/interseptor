package store

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxNotesImageBytes = 5 << 20 // 5 MiB per pasted screenshot

// allowedNotesImageMIME is the raster-image allowlist for stored notebook
// images. Anything outside it (text/html, image/svg+xml, …) is coerced to an
// inert type so a malicious MIME can't execute as active content when the image
// is served same-origin from the control plane (stored-XSS prevention).
var allowedNotesImageMIME = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
	"image/avif": true,
}

// SanitizeNotesImageMIME returns mime when it is an allowlisted raster image
// type, otherwise "application/octet-stream" (served inert — never as HTML, SVG
// or script). Applied both on insert and on serve, so already-stored rows with
// a dangerous MIME are also neutralized.
func SanitizeNotesImageMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 { // drop "; charset=…"
		mime = strings.TrimSpace(mime[:i])
	}
	if allowedNotesImageMIME[mime] {
		return mime
	}
	return "application/octet-stream"
}

var (
	notesDataImageRE = regexp.MustCompile(`!\[([^\]]*)\]\(data:(image/[a-zA-Z0-9.+-]+);base64,([A-Za-z0-9+/=\s]+)\)`)
	notesImgRefRE    = regexp.MustCompile(`/api/notes/images/(\d+)`)
)

// InsertNotesImage stores one embedded notebook image and returns its row id.
func (s *Store) InsertNotesImage(mime string, data []byte) (int64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty image")
	}
	if len(data) > maxNotesImageBytes {
		return 0, fmt.Errorf("image too large (max %d bytes)", maxNotesImageBytes)
	}
	mime = SanitizeNotesImageMIME(mime)
	res, err := s.db.Exec(
		`INSERT INTO notes_images (ts, mime, data) VALUES (?, ?, ?)`,
		time.Now().UnixMilli(), mime, data)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetNotesImage loads a stored notebook image by id.
func (s *Store) GetNotesImage(id int64) (mime string, data []byte, err error) {
	err = s.db.QueryRow(`SELECT mime, data FROM notes_images WHERE id = ?`, id).Scan(&mime, &data)
	return mime, data, err
}

// NormalizeNotesMarkdown replaces inline data-URL images with /api/notes/images/{id}
// references backed by SQLite blobs, so the markdown stays small.
func (s *Store) NormalizeNotesMarkdown(notes string) (string, error) {
	if !strings.Contains(notes, "data:image/") {
		return notes, nil
	}
	var firstErr error
	out := notesDataImageRE.ReplaceAllStringFunc(notes, func(match string) string {
		if firstErr != nil {
			return match
		}
		subs := notesDataImageRE.FindStringSubmatch(match)
		if len(subs) != 4 {
			return match
		}
		raw := strings.ReplaceAll(subs[3], "\n", "")
		raw = strings.ReplaceAll(raw, "\r", "")
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			firstErr = fmt.Errorf("invalid pasted image data: %w", err)
			return match
		}
		id, err := s.InsertNotesImage(subs[2], data)
		if err != nil {
			firstErr = err
			return match
		}
		return fmt.Sprintf("![%s](/api/notes/images/%d)", subs[1], id)
	})
	return out, firstErr
}

// GCNotesImages deletes notebook images no longer referenced in the markdown.
func (s *Store) GCNotesImages(notes string) error {
	used := map[int64]bool{}
	for _, m := range notesImgRefRE.FindAllStringSubmatch(notes, -1) {
		if len(m) != 2 {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		used[id] = true
	}
	rows, err := s.db.Query(`SELECT id FROM notes_images`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var orphans []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if !used[id] {
			orphans = append(orphans, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range orphans {
		if _, err := s.db.Exec(`DELETE FROM notes_images WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// PersistNotes saves normalized markdown and drops orphaned images.
func (s *Store) PersistNotes(notes string) (string, error) {
	normalized, err := s.NormalizeNotesMarkdown(notes)
	if err != nil {
		return "", err
	}
	if err := s.GCNotesImages(normalized); err != nil {
		return "", err
	}
	if err := s.SetSetting("project.notes", normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// LoadNotes returns project notes, migrating any legacy inline data-URL images.
func (s *Store) LoadNotes() (string, error) {
	notes, _, err := s.GetSetting("project.notes")
	if err != nil {
		return "", err
	}
	if !strings.Contains(notes, "data:image/") {
		return notes, nil
	}
	return s.PersistNotes(notes)
}

// DecodeNotesImagePayload decodes a base64 image upload body.
func DecodeNotesImagePayload(mime, b64 string) (string, []byte, error) {
	mime = strings.TrimSpace(mime)
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return "", nil, fmt.Errorf("image data is required")
	}
	if strings.HasPrefix(b64, "data:") {
		const sep = ";base64,"
		i := strings.Index(b64, sep)
		if i < 0 {
			return "", nil, fmt.Errorf("invalid data URL")
		}
		if mime == "" {
			mime = strings.TrimPrefix(b64[:i], "data:")
		}
		b64 = b64[i+len(sep):]
	}
	raw := strings.ReplaceAll(strings.ReplaceAll(b64, "\n", ""), "\r", "")
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", nil, fmt.Errorf("invalid base64 image data")
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return mime, data, nil
}

// NotesImageExists reports whether an image id is present (for tests).
func (s *Store) NotesImageExists(id int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM notes_images WHERE id = ?`, id).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return n > 0, err
}
