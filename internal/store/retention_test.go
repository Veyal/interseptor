package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustInsertFlow(t *testing.T, s *Store, host string, reqHash, resHash string, reqLen, resLen int64) int64 {
	t.Helper()
	id, err := s.InsertFlow(&Flow{
		TS:          time.UnixMilli(1),
		Method:      "GET",
		Host:        host,
		Path:        "/",
		ReqBodyHash: reqHash,
		ResBodyHash: resHash,
		ReqLen:      reqLen,
		ResLen:      resLen,
	})
	if err != nil {
		t.Fatalf("InsertFlow(%s): %v", host, err)
	}
	return id
}

// writeBody writes data into the store's body directory and returns the hash.
func writeBody(t *testing.T, s *Store, data string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatalf("NewBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatalf("write body: %v", err)
	}
	h, _, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return h
}

// ---------------------------------------------------------------------------
// DeleteFlowsByHost
// ---------------------------------------------------------------------------

func TestDeleteFlowsByHost_ExactMatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	mustInsertFlow(t, s, "example.com", "", "", 0, 0)
	mustInsertFlow(t, s, "other.com", "", "", 0, 0)

	n, err := s.DeleteFlowsByHost([]string{"example.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}

	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 || remaining[0].Host != "other.com" {
		t.Fatalf("expected only other.com remaining, got %v", remaining)
	}
}

func TestDeleteFlowsByHost_WildcardSubdomain(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "api.example.com", "", "", 0, 0)
	mustInsertFlow(t, s, "cdn.example.com", "", "", 0, 0)
	mustInsertFlow(t, s, "example.com", "", "", 0, 0) // base domain also matches *.example.com
	mustInsertFlow(t, s, "other.com", "", "", 0, 0)

	n, err := s.DeleteFlowsByHost([]string{"*.example.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 3 {
		t.Fatalf("deleted %d, want 3", n)
	}

	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 || remaining[0].Host != "other.com" {
		t.Fatalf("expected only other.com remaining, got %v", remaining)
	}
}

func TestDeleteFlowsByHost_CaseInsensitive(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "Example.COM", "", "", 0, 0)
	mustInsertFlow(t, s, "EXAMPLE.com", "", "", 0, 0)

	n, err := s.DeleteFlowsByHost([]string{"example.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}
}

func TestDeleteFlowsByHost_KeepOnly(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "keep.com", "", "", 0, 0)
	mustInsertFlow(t, s, "keep.com", "", "", 0, 0)
	mustInsertFlow(t, s, "purge.com", "", "", 0, 0)
	mustInsertFlow(t, s, "also-purge.com", "", "", 0, 0)

	// keepOnly=true: delete everything that does NOT match "keep.com"
	n, err := s.DeleteFlowsByHost([]string{"keep.com"}, true)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost keepOnly: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}

	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}
	for _, f := range remaining {
		if f.Host != "keep.com" {
			t.Fatalf("unexpected host %q in remaining", f.Host)
		}
	}
}

func TestDeleteFlowsByHost_KeepOnlyWithWildcard(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "api.keep.com", "", "", 0, 0)
	mustInsertFlow(t, s, "keep.com", "", "", 0, 0)
	mustInsertFlow(t, s, "purge.com", "", "", 0, 0)

	n, err := s.DeleteFlowsByHost([]string{"*.keep.com"}, true)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost keepOnly wildcard: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d, want 1", n)
	}

	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}
}

func TestDeleteFlowsByHost_EmptyListNoOp(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "example.com", "", "", 0, 0)

	// keepOnly=false, empty list → no-op
	n, err := s.DeleteFlowsByHost([]string{}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost empty no-op: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 deleted, got %d", n)
	}

	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining after no-op, got %d", len(remaining))
	}
}

func TestDeleteFlowsByHost_EmptyKeepListGuard(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "example.com", "", "", 0, 0)

	// keepOnly=true, empty list → must return an error (safety guard)
	_, err = s.DeleteFlowsByHost([]string{}, true)
	if err == nil {
		t.Fatal("expected error for keepOnly with empty host list, got nil")
	}

	// Flows must be untouched
	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 {
		t.Fatalf("data must be untouched after guard error, got %d rows", len(remaining))
	}
}

func TestDeleteFlowsByHost_NonMatchingUntouched(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "a.com", "", "", 0, 0)
	mustInsertFlow(t, s, "b.com", "", "", 0, 0)

	// Delete only a.com — b.com must survive
	n, err := s.DeleteFlowsByHost([]string{"a.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d, want 1", n)
	}
	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 || remaining[0].Host != "b.com" {
		t.Fatalf("expected only b.com, got %v", remaining)
	}
}

func TestDeleteFlowsByHost_MultiplePatterns(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "a.com", "", "", 0, 0)
	mustInsertFlow(t, s, "b.com", "", "", 0, 0)
	mustInsertFlow(t, s, "c.com", "", "", 0, 0)

	n, err := s.DeleteFlowsByHost([]string{"a.com", "b.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d, want 2", n)
	}
	remaining, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100})
	if len(remaining) != 1 || remaining[0].Host != "c.com" {
		t.Fatalf("expected only c.com, got %v", remaining)
	}
}

// ---------------------------------------------------------------------------
// GCBodies
// ---------------------------------------------------------------------------

func TestGCBodies_RemovesOrphanedBody(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Write a body but never reference it in any flow.
	orphanHash := writeBody(t, s, "orphaned body content")

	// Confirm the file exists before GC.
	orphanPath := s.bodyPath(orphanHash)
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("orphan body file missing before GC: %v", err)
	}

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}
	expectedBytes := int64(len("orphaned body content"))
	if freed != expectedBytes {
		t.Fatalf("freed %d bytes, want %d", freed, expectedBytes)
	}

	// File must be gone after GC.
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan body file still exists after GC")
	}
}

func TestGCBodies_PreservesReferencedBody(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Write a body and reference it from a flow.
	refHash := writeBody(t, s, "referenced body")
	mustInsertFlow(t, s, "example.com", refHash, "", int64(len("referenced body")), 0)

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d, want 0 (body still referenced)", removed)
	}
	if freed != 0 {
		t.Fatalf("freed %d bytes, want 0", freed)
	}

	// File must still exist.
	if _, err := os.Stat(s.bodyPath(refHash)); err != nil {
		t.Fatalf("referenced body file removed by GC: %v", err)
	}
}

func TestGCBodies_PreservesFinalizedBodyUntilFlowPublishesHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	flow := &Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"}
	if _, err := s.InsertFlow(flow); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, "published after finalize"); err != nil {
		t.Fatalf("write body: %v", err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed %d body before UpdateFlow published its hash", removed)
	}
	flow.ResBodyHash = hash
	flow.ResLen = int64(len("published after finalize"))
	if err := s.UpdateFlow(flow); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}
	rc, err := s.OpenBody(hash)
	if err != nil {
		t.Fatalf("OpenBody after publication: %v", err)
	}
	rc.Close()
}

func TestGCBodies_PendingOwnershipIsReferenceCounted(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	finalize := func() (*BodyWriter, string) {
		t.Helper()
		w, err := s.NewFlowBodyWriter()
		if err != nil {
			t.Fatalf("NewFlowBodyWriter: %v", err)
		}
		if _, err := io.WriteString(w, "same pending body"); err != nil {
			t.Fatalf("write: %v", err)
		}
		hash, _, err := w.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		return w, hash
	}
	_, hash := finalize()
	_, secondHash := finalize()
	if secondHash != hash {
		t.Fatalf("hashes differ: %s != %s", hash, secondHash)
	}
	flowID := mustInsertFlow(t, s, "example.com", "", hash, 0, int64(len("same pending body")))
	if _, err := s.DeleteFlows([]int64{flowID}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed body still owned by second writer")
	}
}

func TestGCBodies_ReclaimsFinalizedBodyAfterExplicitAbort(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	io.WriteString(w, "abandoned finalized body")
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	w.Abort()

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want abandoned body reclaimed", removed)
	}
	if _, err := s.OpenBody(hash); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenBody after GC = %v, want not exist", err)
	}
}

func TestGCBodies_ConcurrentGCWaitsForDelayedPublication(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	flow := &Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"}
	if _, err := s.InsertFlow(flow); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	io.WriteString(w, "delayed publication")
	hash, size, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	gcDone := make(chan error, 1)
	go func() {
		for range 20 {
			if _, _, err := s.GCBodies(); err != nil {
				gcDone <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
		gcDone <- nil
	}()
	time.Sleep(10 * time.Millisecond)
	flow.ResBodyHash, flow.ResLen = hash, size
	if err := s.UpdateFlow(flow); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}
	if err := <-gcDone; err != nil {
		t.Fatalf("concurrent GCBodies: %v", err)
	}
	rc, err := s.OpenBody(hash)
	if err != nil {
		t.Fatalf("OpenBody after delayed publication: %v", err)
	}
	rc.Close()
}

func TestGCBodies_ReferencedByResHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	resHash := writeBody(t, s, "response body")
	mustInsertFlow(t, s, "example.com", "", resHash, 0, int64(len("response body")))

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d, want 0 (res body still referenced)", removed)
	}
}

func TestGCBodies_IdempotentSecondRunFreesZero(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	writeBody(t, s, "orphaned again") // unreferenced

	// First GC: removes it.
	removed1, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies first: %v", err)
	}
	if removed1 != 1 {
		t.Fatalf("first GC removed %d, want 1", removed1)
	}

	// Second GC: nothing left to remove.
	removed2, freed2, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies second: %v", err)
	}
	if removed2 != 0 || freed2 != 0 {
		t.Fatalf("second GC removed=%d freed=%d, both want 0", removed2, freed2)
	}
}

func TestGCBodies_MixedOrphanAndReferenced(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	orphanHash := writeBody(t, s, "this one is orphaned")
	refHash := writeBody(t, s, "this one is referenced")
	mustInsertFlow(t, s, "example.com", refHash, "", int64(len("this one is referenced")), 0)

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}
	if freed != int64(len("this one is orphaned")) {
		t.Fatalf("freed %d, want %d", freed, len("this one is orphaned"))
	}

	// Orphan gone, referenced still present.
	if _, err := os.Stat(s.bodyPath(orphanHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan still present after GC")
	}
	if _, err := os.Stat(s.bodyPath(refHash)); err != nil {
		t.Fatalf("referenced body removed by GC: %v", err)
	}
}

func TestGCBodies_IgnoresTempFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Simulate a leftover .tmp- file (aborted upload) directly in bodiesDir.
	tmpPath := filepath.Join(s.bodiesDir, ".tmp-stale")
	if err := os.WriteFile(tmpPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	// GC must not touch the .tmp- file (not a content-hash path).
	if removed != 0 {
		t.Fatalf("GC should not count/remove tmp files, removed=%d", removed)
	}
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("tmp file was removed by GC: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HostStats
// ---------------------------------------------------------------------------

func TestHostStats_Basic(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "alpha.com", "", "", 100, 200) // 300 bytes total
	mustInsertFlow(t, s, "alpha.com", "", "", 50, 50)   // 100 bytes → alpha total = 400
	mustInsertFlow(t, s, "beta.com", "", "", 500, 500)  // 1000 bytes

	stats, err := s.HostStats()
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 host entries, got %d", len(stats))
	}

	// Sorted descending by bytes: beta.com (1000) then alpha.com (400).
	if stats[0].Host != "beta.com" {
		t.Fatalf("expected beta.com first (highest bytes), got %q", stats[0].Host)
	}
	if stats[0].Bytes != 1000 {
		t.Fatalf("beta.com bytes = %d, want 1000", stats[0].Bytes)
	}
	if stats[0].Flows != 1 {
		t.Fatalf("beta.com flows = %d, want 1", stats[0].Flows)
	}
	if stats[1].Host != "alpha.com" {
		t.Fatalf("expected alpha.com second, got %q", stats[1].Host)
	}
	if stats[1].Bytes != 400 {
		t.Fatalf("alpha.com bytes = %d, want 400", stats[1].Bytes)
	}
	if stats[1].Flows != 2 {
		t.Fatalf("alpha.com flows = %d, want 2", stats[1].Flows)
	}
}

func TestHostStats_EmptyStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	stats, err := s.HostStats()
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected 0 entries on empty store, got %d", len(stats))
	}
}

func TestHostStats_SingleHost(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "only.com", "", "", 10, 20)

	stats, err := s.HostStats()
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(stats))
	}
	if stats[0].Host != "only.com" || stats[0].Flows != 1 || stats[0].Bytes != 30 {
		t.Fatalf("unexpected stat: %+v", stats[0])
	}
}
