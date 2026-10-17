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
//
// The temp file is created on the first non-empty Write, so an empty body
// (every GET request, every 204/304 response) costs no file system work and
// finalizes to "" — the same "no body" the other capture paths record.
type BodyWriter struct {
	s   *Store
	tmp *os.File
	h   hash.Hash
	n   int64
	// createErr is a failed lazy temp-file creation. Write keeps reporting
	// success so a tee'd forward is never broken by capture; Finalize returns
	// the error so the caller flags the flow.
	createErr          error
	pendingPublication bool
	// lockHeld marks a writer whose caller already holds bodyMu and keeps
	// holding it until the hash is referenced. Finalize must not take the lock
	// again — Go mutexes are not reentrant.
	lockHeld    bool
	pendingHash string
}

// NewBodyWriter starts a new body capture.
func (s *Store) NewBodyWriter() (*BodyWriter, error) {
	return s.newBodyWriter(false, false)
}

// NewFlowBodyWriter starts a capture whose finalized blob is protected from
// body GC until InsertFlow or UpdateFlow publishes the returned hash.
func (s *Store) NewFlowBodyWriter() (*BodyWriter, error) {
	return s.newBodyWriter(true, false)
}

// newBodyWriterUnderBodyLock starts a capture for a caller that already holds
// bodyMu across the whole finalize-to-reference window.
func (s *Store) newBodyWriterUnderBodyLock() (*BodyWriter, error) {
	return s.newBodyWriter(false, true)
}

func (s *Store) newBodyWriter(pendingPublication, lockHeld bool) (*BodyWriter, error) {
	return &BodyWriter{
		s: s, h: sha256.New(),
		pendingPublication: pendingPublication,
		lockHeld:           lockHeld,
	}, nil
}

// Write implements io.Writer.
func (w *BodyWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.tmp == nil {
		if w.createErr != nil {
			return len(p), nil
		}
		tmp, err := os.CreateTemp(w.s.bodiesDir, ".tmp-*")
		if err != nil {
			w.createErr = err
			return len(p), nil
		}
		w.tmp = tmp
	}
	n, err := w.tmp.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, err
}

// Finalize commits the body and returns its sha256 hex hash and byte length.
// If a body with the same hash already exists it is deduplicated.
func (w *BodyWriter) Finalize() (string, int64, error) {
	if w.tmp == nil {
		// Nothing was written, or the temp file could not be created: no
		// file, no hash and no GC claim to take.
		return "", 0, w.createErr
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
	if err := w.claim(sum); err != nil {
		return "", 0, err
	}
	// The file system work runs after the claim and outside bodyMu. The claim
	// already keeps a GC pass from reclaiming the blob — including when the
	// rename deduplicates onto a file a running walk has listed, because removal
	// re-checks ownership under bodyMu — so the lock only has to cover the
	// bookkeeping. Holding the store-wide lock across stat, rename and unlink
	// serialised every capture behind the slowest file system call, which
	// stalled proxied requests for seconds under load.
	if err := w.commit(tmpName, sum); err != nil {
		w.unclaim()
		return "", 0, err
	}
	return sum, w.n, nil
}

// bodyCommitHook, when set by a test, runs at the start of the lock-free file
// system phase of Finalize.
var bodyCommitHook func()

// claim records ownership of sum under bodyMu (taken here unless the caller
// already holds it): the pending-publication count for flow captures, and the
// claim a running GC pass checks before it removes a file.
func (w *BodyWriter) claim(sum string) error {
	if !w.lockHeld {
		w.s.bodyMu.Lock()
		defer w.s.bodyMu.Unlock()
	}
	if err := w.protectPending(sum); err != nil {
		return err
	}
	w.s.noteBodyGCClaimLocked(sum)
	return nil
}

// unclaim drops the pending-publication claim of a body that failed to commit.
func (w *BodyWriter) unclaim() {
	if !w.pendingPublication || w.pendingHash == "" {
		return
	}
	if !w.lockHeld {
		w.s.bodyMu.Lock()
		defer w.s.bodyMu.Unlock()
	}
	w.s.releasePendingBody(w.pendingHash)
	w.pendingHash = ""
}

// commit moves the finished temp file to its content-addressed path, or
// deduplicates onto an identical body that is already stored.
func (w *BodyWriter) commit(tmpName, sum string) error {
	if bodyCommitHook != nil {
		bodyCommitHook()
	}
	dst := w.s.bodyPath(sum)
	if _, err := os.Stat(dst); err == nil {
		return nil // identical body already stored; the caller removes the temp
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		// A concurrent writer with identical content may have created dst
		// between our Stat above and this Rename. On Windows, Rename onto an
		// existing file fails; treat an already-present dst as a successful
		// dedup rather than a spurious capture error.
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return err
	}
	return nil
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
	// Most flows carry no body claim (GETs, an enqueue that dropped nothing).
	// bodyMu is held across file system calls by every body capture, so never
	// queue behind it for nothing.
	if !hasAnyHash(hashes) {
		return
	}
	s.bodyMu.Lock()
	for _, sum := range hashes {
		s.releasePendingBody(sum)
	}
	s.bodyMu.Unlock()
}

func hasAnyHash(hashes []string) bool {
	for _, sum := range hashes {
		if sum != "" {
			return true
		}
	}
	return false
}

func (s *Store) protectMergeBodies(hashes []string) func() {
	s.bodyMu.Lock()
	if s.mergeBodies == nil {
		s.mergeBodies = make(map[string]int)
	}
	for _, sum := range hashes {
		s.mergeBodies[sum]++
		s.noteBodyGCClaimLocked(sum)
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
	if w.tmp != nil {
		w.tmp.Close()
		os.Remove(w.tmp.Name())
	}
	if w.pendingHash == "" {
		return
	}
	if !w.lockHeld {
		w.s.bodyMu.Lock()
		defer w.s.bodyMu.Unlock()
	}
	w.s.releasePendingBody(w.pendingHash)
	w.pendingHash = ""
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
