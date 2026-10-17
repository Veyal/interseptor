package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// TestTeeBodyNoBodyPassesThrough pins that http.NoBody (what net/http gives every
// server-side GET) is not wrapped: the caller keeps forwarding NoBody itself, so
// the outbound Transport does not probe a wrapped empty body, and finalize
// reports "no body".
func TestTeeBodyNoBodyPassesThrough(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	tee, finalize, err := New(s).TeeBody(http.NoBody)
	if err != nil {
		t.Fatalf("TeeBody: %v", err)
	}
	if tee != nil {
		t.Fatalf("TeeBody(http.NoBody) wrapped the body: %T", tee)
	}
	if h, n, err := finalize(); h != "" || n != 0 || err != nil {
		t.Fatalf("finalize = %q, %d, %v", h, n, err)
	}
}

func TestTeeBodyStreamsAndStores(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	c := New(s)

	const payload = "the quick brown fox"
	tee, finalize, err := c.TeeBody(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("TeeBody: %v", err)
	}

	// Reading the tee yields the original bytes (proves streaming pass-through).
	got, _ := io.ReadAll(tee)
	if string(got) != payload {
		t.Fatalf("tee passthrough mismatch: %q", got)
	}

	hash, n, err := finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	want := sha256.Sum256([]byte(payload))
	if hash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash mismatch: %s", hash)
	}
	if n != int64(len(payload)) {
		t.Fatalf("len mismatch: %d", n)
	}

	rc, err := s.OpenBody(hash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	stored, _ := io.ReadAll(rc)
	if string(stored) != payload {
		t.Fatalf("stored body mismatch: %q", stored)
	}
}
