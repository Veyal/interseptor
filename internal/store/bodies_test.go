package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestBodyWriterEmptyBodyCreatesNoFile pins the empty-body fast path: a capture
// that never receives a byte (every GET request body, every 204/304 response)
// must not create, hash, rename or unlink a temp file, and reports "no body"
// rather than the sha256 of the empty string.
func TestBodyWriterEmptyBodyCreatesNoFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	for _, mk := range []func() (*BodyWriter, error){s.NewBodyWriter, s.NewFlowBodyWriter} {
		w, err := mk()
		if err != nil {
			t.Fatalf("new writer: %v", err)
		}
		if _, err := w.Write(nil); err != nil {
			t.Fatalf("empty write: %v", err)
		}
		if tmp, _ := filepath.Glob(filepath.Join(s.BodiesDir(), ".tmp-*")); len(tmp) != 0 {
			t.Fatalf("empty capture created temp files %v", tmp)
		}
		hash, n, err := w.Finalize()
		if err != nil || hash != "" || n != 0 {
			t.Fatalf("Finalize = %q, %d, %v; want \"\", 0, nil", hash, n, err)
		}
		w.Abort() // safe after Finalize on the empty path
	}
	entries, err := os.ReadDir(s.BodiesDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("bodies dir not empty after empty captures: %d entries", len(entries))
	}
	s.bodyMu.Lock()
	pending := len(s.pendingBodies)
	s.bodyMu.Unlock()
	if pending != 0 {
		t.Fatalf("empty capture left %d pending ownership record(s)", pending)
	}
}

func TestFlowBodyWriterReleasesOwnershipWhenFinalizeFails(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	const content = "cannot finalize"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(s.BodiesDir(), hash[:2]), []byte("blocks directory"), 0o644); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	io.WriteString(w, content)
	if _, _, err := w.Finalize(); err == nil {
		t.Fatal("Finalize succeeded despite blocked destination directory")
	}
	s.bodyMu.Lock()
	pending := len(s.pendingBodies)
	s.bodyMu.Unlock()
	if pending != 0 {
		t.Fatalf("failed finalize leaked %d pending ownership record(s)", pending)
	}
}

func TestBodyWriterStoreDedupAndRead(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	write := func(data string) (string, int64) {
		w, err := s.NewBodyWriter()
		if err != nil {
			t.Fatalf("NewBodyWriter: %v", err)
		}
		if _, err := io.WriteString(w, data); err != nil {
			t.Fatalf("write: %v", err)
		}
		hash, n, err := w.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		return hash, n
	}

	h1, n1 := write("hello world")
	h2, n2 := write("hello world") // identical -> dedup, same hash
	if h1 != h2 {
		t.Fatalf("expected identical hashes, got %s vs %s", h1, h2)
	}
	if n1 != 11 || n2 != 11 {
		t.Fatalf("expected len 11, got %d/%d", n1, n2)
	}

	rc, err := s.OpenBody(h1)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello world" {
		t.Fatalf("body mismatch: %q", got)
	}

	empty, err := s.OpenBody("")
	if err != nil {
		t.Fatalf("OpenBody empty: %v", err)
	}
	defer empty.Close()
	if b, _ := io.ReadAll(empty); len(b) != 0 {
		t.Fatalf("expected empty body for empty hash, got %q", b)
	}
}

// OpenBody must never panic or escape bodiesDir on a malformed hash (e.g. a
// short or traversal string imported from a crafted HAR). Such hashes are
// treated as "no such body" (os.ErrNotExist), not a crash or arbitrary read.
func TestOpenBodyRejectsMalformedHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for _, bad := range []string{"ab", "x", "../../../etc/passwd", "../../secret", "ZZZ", "g0e1d2c3"} {
		rc, err := s.OpenBody(bad) // must not panic
		if rc != nil {
			rc.Close()
			t.Fatalf("OpenBody(%q) returned a reader; want error", bad)
		}
		if !os.IsNotExist(err) {
			t.Fatalf("OpenBody(%q) err = %v, want os.ErrNotExist", bad, err)
		}
	}

	// An empty hash is still a valid "no body" → empty reader, no error.
	rc, err := s.OpenBody("")
	if err != nil {
		t.Fatalf("OpenBody(\"\"): %v", err)
	}
	rc.Close()
}

// Concurrent finalizes of identical content must all dedup to the same hash
// with no error. On Windows, os.Rename onto an existing destination fails, so
// the loser of the create race previously got a spurious capture error.
func TestBodyWriterConcurrentDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const n = 16
	var wg sync.WaitGroup
	hashes := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := s.NewBodyWriter()
			if err != nil {
				errs[i] = err
				return
			}
			io.WriteString(w, "the same body for everyone")
			<-start
			h, _, err := w.Finalize()
			hashes[i], errs[i] = h, err
		}(i)
	}
	close(start) // release all finalizes as simultaneously as possible
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: Finalize error: %v", i, errs[i])
		}
		if hashes[i] != hashes[0] {
			t.Fatalf("goroutine %d: hash %s != %s", i, hashes[i], hashes[0])
		}
	}
}
