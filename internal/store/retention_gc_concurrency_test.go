package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// gcWalkOrphans is how many unreferenced body files the concurrency tests put
// on disk, and gcWalkCaptures bounds how many bodies they capture while the
// pass runs. A real engagement reaches tens of thousands of blobs; these
// numbers are the smallest that still separate the two behaviours, so the suite
// stays quick under -race. The walk takes a few hundred milliseconds here and
// one capture takes well under a millisecond, so the lock-free walk gets
// hundreds of captures through while holding the lock gets exactly one: the one
// already blocked when the walk ended.
const (
	gcWalkOrphans  = 6000
	gcWalkCaptures = 300
)

// seedOrphanBodies writes n unreferenced body files in the content-addressed
// layout, spread across all 256 top-level prefixes the way real hashes are.
func seedOrphanBodies(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		sum := sha256.Sum256([]byte("orphan-" + strconv.Itoa(i)))
		p := s.bodyPath(hex.EncodeToString(sum[:]))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("orphan"), 0o644); err != nil {
			t.Fatalf("write orphan: %v", err)
		}
	}
}

// captureBody finalizes one flow body and publishes it through a flow row, the
// way the proxy does.
func captureBody(t *testing.T, s *Store, payload string) string {
	t.Helper()
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, payload); err != nil {
		t.Fatalf("write body: %v", err)
	}
	hash, size, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	f := &Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com",
		Path: "/captured", ResBodyHash: hash, ResLen: size,
	}
	if _, err := s.InsertFlow(f); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return hash
}

// TestGCBodiesDoesNotBlockCaptureDuringWalk is the proxy-stall fix: the walk
// over a large bodies directory must not hold bodyMu, which every captured
// body takes on Finalize. With the lock held for the whole walk, capture gets
// through at most once — the write that was already blocked when the walk
// ended.
func TestGCBodiesDoesNotBlockCaptureDuringWalk(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedOrphanBodies(t, s, gcWalkOrphans)

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		if _, _, err := s.GCBodies(); err != nil {
			t.Errorf("GCBodies: %v", err)
		}
	}()
	<-started

	captured := 0
	var slowest time.Duration
	for captured < gcWalkCaptures && !closed(done) {
		w, err := s.NewFlowBodyWriter()
		if err != nil {
			t.Fatalf("NewFlowBodyWriter: %v", err)
		}
		if _, err := io.WriteString(w, "concurrent-"+strconv.Itoa(captured)); err != nil {
			t.Fatalf("write: %v", err)
		}
		start := time.Now()
		if _, _, err := w.Finalize(); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if d := time.Since(start); d > slowest {
			slowest = d
		}
		w.Abort() // hand the ownership back; this test is about blocking, not retention
		captured++
	}
	<-done
	if captured < 50 {
		t.Fatalf("only %d bodies were captured while GC walked %d files; "+
			"the walk is holding bodyMu (slowest capture %s)", captured, gcWalkOrphans, slowest)
	}
	t.Logf("captured %d bodies while GC walked %d files; slowest capture %s",
		captured, gcWalkOrphans, slowest)
}

// closed reports whether the GC goroutine has finished.
func closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// TestGCBodiesKeepsBodiesCapturedDuringWalk covers the race releasing the lock
// opens. A body finalized and published after the pass took its reference
// snapshot appears in neither the snapshot nor the database query, yet the walk
// will find its file. It must survive.
func TestGCBodiesKeepsBodiesCapturedDuringWalk(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedOrphanBodies(t, s, gcWalkOrphans)

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		if _, _, err := s.GCBodies(); err != nil {
			t.Errorf("GCBodies: %v", err)
		}
	}()
	<-started

	var hashes []string
	for len(hashes) < gcWalkCaptures && !closed(done) {
		hashes = append(hashes, captureBody(t, s, "mid-walk-"+strconv.Itoa(len(hashes))))
	}
	<-done
	if len(hashes) < 20 {
		t.Fatalf("only %d bodies were captured during the walk; test is inconclusive", len(hashes))
	}
	for i, h := range hashes {
		rc, err := s.OpenBody(h)
		if err != nil {
			t.Fatalf("body %d of %d captured during the walk was deleted: %v", i, len(hashes), err)
		}
		rc.Close()
	}
	t.Logf("%d bodies captured during the walk all survived", len(hashes))
}

// TestGCBodiesKeepsBodyRepublishedDuringWalk is the subtle half of that race.
// The body file already exists and is unreferenced when the pass starts, so it
// is a delete candidate. Mid-walk a capture produces the same bytes: Finalize
// takes ownership, sees the file already there, deduplicates onto it, and a
// flow row starts referencing it. Neither the snapshot nor the query can know,
// so the pass has to remember that something took ownership while it ran.
func TestGCBodiesKeepsBodyRepublishedDuringWalk(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Payload whose hash sorts into the last top-level prefix, so the walk
	// reaches its file at the very end and the republish below lands first.
	payload, hash := payloadHashedUnderPrefix(t, "ff")
	p := s.bodyPath(hash)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(payload), 0o644); err != nil {
		t.Fatalf("seed body: %v", err)
	}
	seedOrphanBodies(t, s, gcWalkOrphans)

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		if _, _, err := s.GCBodies(); err != nil {
			t.Errorf("GCBodies: %v", err)
		}
	}()
	<-started

	if got := captureBody(t, s, payload); got != hash {
		t.Fatalf("republished hash = %s, want %s", got, hash)
	}
	<-done

	rc, err := s.OpenBody(hash)
	if err != nil {
		t.Fatalf("body republished during the walk was deleted: %v", err)
	}
	rc.Close()
}

// payloadHashedUnderPrefix finds a short payload whose sha256 hex starts with
// prefix, so a test can place its body file at a known point in the walk order.
func payloadHashedUnderPrefix(t *testing.T, prefix string) (payload, hash string) {
	t.Helper()
	for i := 0; i < 1<<20; i++ {
		p := "republished-" + strconv.Itoa(i)
		sum := sha256.Sum256([]byte(p))
		h := hex.EncodeToString(sum[:])
		if h[:len(prefix)] == prefix {
			return p, h
		}
	}
	t.Fatalf("no payload hashed under prefix %q", prefix)
	return "", ""
}
