package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	s                  *Store
	tmp                *os.File
	h                  hash.Hash
	n                  int64
	pendingPublication bool
	pendingHash        string
}

// NewBodyWriter starts a new body capture.
func (s *Store) NewBodyWriter() (*BodyWriter, error) {
	return s.newBodyWriter(false)
}

// NewFlowBodyWriter starts a capture whose finalized blob is protected from
// body GC until InsertFlow or UpdateFlow publishes the returned hash.
func (s *Store) NewFlowBodyWriter() (*BodyWriter, error) {
	return s.newBodyWriter(true)
}

func (s *Store) newBodyWriter(pendingPublication bool) (*BodyWriter, error) {
	tmp, err := os.CreateTemp(s.bodiesDir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &BodyWriter{s: s, tmp: tmp, h: sha256.New(), pendingPublication: pendingPublication}, nil
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
	if w.pendingPublication {
		w.s.bodyMu.Lock()
		defer w.s.bodyMu.Unlock()
	}
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
	if err := w.protectPending(sum); err != nil {
		return "", 0, err
	}
	finalized := false
	defer func() {
		if w.pendingPublication && !finalized {
			w.s.releasePendingBody(w.pendingHash)
			w.pendingHash = ""
		}
	}()
	dst := w.s.bodyPath(sum)
	if _, err := os.Stat(dst); err == nil {
		finalized = true
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
			finalized = true
			return sum, w.n, nil
		}
		return "", 0, err
	}
	finalized = true
	return sum, w.n, nil
}

const maxPendingBodies = 4096

func (w *BodyWriter) protectPending(sum string) error {
	if !w.pendingPublication {
		return nil
	}
	if w.s.pendingBodies == nil {
		w.s.pendingBodies = make(map[string]int)
	}
	if w.s.pendingBodies[sum] == 0 && len(w.s.pendingBodies) >= maxPendingBodies {
		return fmt.Errorf("store: too many bodies awaiting flow publication")
	}
	w.s.pendingBodies[sum]++
	w.pendingHash = sum
	return nil
}

func (s *Store) releasePendingBody(sum string) {
	if sum == "" {
		return
	}
	if s.pendingBodies[sum] <= 1 {
		delete(s.pendingBodies, sum)
		return
	}
	s.pendingBodies[sum]--
}

func (s *Store) publishBodies(hashes ...string) {
	s.bodyMu.Lock()
	for _, sum := range hashes {
		s.releasePendingBody(sum)
	}
	s.bodyMu.Unlock()
}

func (s *Store) protectMergeBodies(hashes []string) func() {
	s.bodyMu.Lock()
	if s.mergeBodies == nil {
		s.mergeBodies = make(map[string]int)
	}
	for _, sum := range hashes {
		s.mergeBodies[sum]++
	}
	s.bodyMu.Unlock()
	return func() {
		s.bodyMu.Lock()
		for _, sum := range hashes {
			if s.mergeBodies[sum] <= 1 {
				delete(s.mergeBodies, sum)
			} else {
				s.mergeBodies[sum]--
			}
		}
		s.bodyMu.Unlock()
	}
}

// Abort discards an in-progress body (e.g. on error).
func (w *BodyWriter) Abort() {
	w.tmp.Close()
	os.Remove(w.tmp.Name())
	if w.pendingHash != "" {
		w.s.bodyMu.Lock()
		w.s.releasePendingBody(w.pendingHash)
		w.s.bodyMu.Unlock()
		w.pendingHash = ""
	}
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
