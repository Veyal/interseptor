package store

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BodyWriter streams bytes to a temp file while hashing them, then commits
// the file to a content-addressed path on Finalize. Safe for bounded memory:
// bytes are never buffered whole.
type BodyWriter struct {
	s   *Store
	tmp *os.File
	h   hash.Hash
	n   int64
}

// NewBodyWriter starts a new body capture.
func (s *Store) NewBodyWriter() (*BodyWriter, error) {
	tmp, err := os.CreateTemp(s.bodiesDir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &BodyWriter{s: s, tmp: tmp, h: sha256.New()}, nil
}

// Write implements io.Writer.
func (w *BodyWriter) Write(p []byte) (int, error) {
	n, err := w.tmp.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, err
}

// Finalize commits the body and returns its sha256 hex hash and byte length.
// If a body with the same hash already exists it is deduplicated.
func (w *BodyWriter) Finalize() (string, int64, error) {
	tmpName := w.tmp.Name()
	if err := w.tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	// Remove the temp file on every path except a successful rename (where it no
	// longer exists, so Remove is a harmless no-op). This cleans up both the
	// dedup path and any MkdirAll/Rename failure after Close.
	defer os.Remove(tmpName)

	sum := hex.EncodeToString(w.h.Sum(nil))
	dst := w.s.bodyPath(sum)
	if _, err := os.Stat(dst); err == nil {
		return sum, w.n, nil // identical body already stored; temp removed by defer
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		// A concurrent writer with identical content may have created dst
		// between our Stat above and this Rename. On Windows, Rename onto an
		// existing file fails; treat an already-present dst as a successful
		// dedup rather than a spurious capture error.
		if _, statErr := os.Stat(dst); statErr == nil {
			return sum, w.n, nil
		}
		return "", 0, err
	}
	return sum, w.n, nil
}

// Abort discards an in-progress body (e.g. on error).
func (w *BodyWriter) Abort() {
	w.tmp.Close()
	os.Remove(w.tmp.Name())
}

func (s *Store) bodyPath(sum string) string {
	return filepath.Join(s.bodiesDir, sum[:2], sum[2:4], sum)
}

// OpenBody returns a reader for the body with the given hash. An empty hash
// yields an empty reader (no body).
func (s *Store) OpenBody(sum string) (io.ReadCloser, error) {
	if sum == "" {
		return io.NopCloser(strings.NewReader("")), nil
	}
	if !isContentHash(sum) {
		// A body hash is always a 64-char lowercase sha256 hex string. Reject
		// anything else: it would either panic bodyPath's sum[:2]/sum[2:4]
		// slicing (e.g. a 2-char hash from a malformed HAR import) or, worse,
		// escape bodiesDir (e.g. "../../etc/passwd"). Treat it as "no such body".
		return nil, os.ErrNotExist
	}
	return os.Open(s.bodyPath(sum))
}

// isContentHash reports whether s is a 64-char lowercase hex sha256 digest — the
// only shape bodyPath may be handed.
func isContentHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
