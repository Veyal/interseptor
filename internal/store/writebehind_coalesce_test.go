package store

import (
	"io"
	"strconv"
	"testing"
	"time"
)

func finalizedBody(t *testing.T, s *Store, content string) string {
	t.Helper()
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatalf("write body: %v", err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return hash
}

func pendingBodyCount(s *Store) int {
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	return len(s.pendingBodies)
}

// TestEnqueueUpdateCoalescesIntoQueuedInsert pins the main capture-throughput
// fix: when a flow's response arrives while its request-time insert is still
// queued, the update replaces the insert's snapshot instead of taking a second
// slot and a second SQLite write. The row lands once, in its final state.
func TestEnqueueUpdateCoalescesIntoQueuedInsert(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)

	f := queueInsert(t, s, "/coalesce")
	if ok, err := s.EnqueueAddFlowTags(f.ID, []string{"auth"}); err != nil || !ok {
		t.Fatalf("enqueue tags: ok=%v err=%v", ok, err)
	}
	f.Status, f.ResLen, f.Mime = 201, 7, "text/plain"
	f.ResHeaders = map[string][]string{"Content-Type": {"text/plain"}}
	if ok, err := s.EnqueueUpdateFlow(f); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if got := s.pendingLen(); got != 2 {
		t.Fatalf("queue holds %d writes, want 2 (insert carrying the update, tags)", got)
	}
	// The caller's flow must not alias the queued snapshot.
	f.Status = 999
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	got, err := s.GetFlow(f.ID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Status != 201 || got.ResLen != 7 || got.Mime != "text/plain" {
		t.Fatalf("persisted %+v, want the coalesced response state", got)
	}
	if tags, _ := s.FlowTags(f.ID); len(tags) != 1 || tags[0] != "auth" {
		t.Fatalf("tags = %v, want [auth]", tags)
	}
	if s.flowsBusy() {
		t.Fatalf("pending bookkeeping still busy after drain")
	}

	// Once the insert has been taken by the flusher, an update appends as before.
	f2 := queueInsert(t, s, "/after-drain")
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	f2.Status = 204
	f2.Path = "/after-drain?edited=1"
	if ok, err := s.EnqueueUpdateFlow(f2); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if got, err := s.GetFlow(f2.ID); err != nil || got.Status != 204 || got.Path != "/after-drain?edited=1" {
		t.Fatalf("appended update not applied: %+v err=%v", got, err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM flows_fts WHERE flows_fts MATCH '"edited"'`); n != 1 {
		t.Fatalf("full-text index missed the updated path: %d hits", n)
	}
	if n := s.FlowsDropped(); n != 0 {
		t.Fatalf("dropped %d", n)
	}
}

// TestEnqueueDoesNotTakeBodyLockWithoutWork pins the hot-path contract: the
// proxy enqueues two writes per flow, and body capture holds bodyMu across
// file system calls, so an enqueue that drops nothing must not touch bodyMu
// (doing so formed multi-second lock convoys under load).
func TestEnqueueDoesNotTakeBodyLockWithoutWork(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)
	s.bodyMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f := queueInsert(t, s, "/no-body-lock")
		f.Status = 200
		s.EnqueueUpdateFlow(f) // coalesced
		g := queueInsert(t, s, "/no-body-lock-2")
		s.flowMu.Lock()
		s.takeFlowPendingLocked() // force the next update to append
		s.flowMu.Unlock()
		g.Status = 200
		s.EnqueueUpdateFlow(g)
		s.EnqueueAddFlowTags(g.ID, []string{"x"})
	}()
	select {
	case <-done:
		s.bodyMu.Unlock()
	case <-time.After(2 * time.Second):
		s.bodyMu.Unlock()
		<-done
		t.Fatal("enqueue blocked on bodyMu although nothing was dropped")
	}
}

// TestCoalescedUpdateReleasesEveryBodyClaim keeps body GC ownership exact when
// two snapshots fold into one queued write: every claim the replaced snapshot
// carried is still released once the coalesced write commits.
func TestCoalescedUpdateReleasesEveryBodyClaim(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)

	f := &Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/claims"}
	f.ReqBodyHash = finalizedBody(t, s, "request body")
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue insert: ok=%v err=%v", ok, err)
	}
	f.ResBodyHash = finalizedBody(t, s, "response body")
	f.Status = 200
	if ok, err := s.EnqueueUpdateFlow(f); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if removed, _, err := s.GCBodies(); err != nil || removed != 0 {
		t.Fatalf("GC removed %d queued bodies (err=%v)", removed, err)
	}
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if n := pendingBodyCount(s); n != 0 {
		t.Fatalf("%d body claims leaked after the coalesced write committed", n)
	}
	if removed, _, err := s.GCBodies(); err != nil || removed != 0 {
		t.Fatalf("GC removed %d referenced bodies (err=%v)", removed, err)
	}
}

// TestFullBufferDropsWholeFlowNotItsUpdate is the status-0 regression: when the
// buffer is full, the overflow must cost one whole not-yet-written flow, never
// the response update of a row that is already in SQLite (which would leave
// that row "pending" forever).
func TestFullBufferDropsWholeFlowNotItsUpdate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)

	committed := queueInsert(t, s, "/committed")
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	committed.Status = 200
	if ok, err := s.EnqueueUpdateFlow(committed); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	var first *Flow
	for i := 0; i < flowBufCap; i++ {
		f := queueInsert(t, s, "/fill-"+strconv.Itoa(i))
		if i == 0 {
			first = f
		}
	}
	if got := s.FlowsDropped(); got != 1 {
		t.Fatalf("dropped %d writes, want 1", got)
	}
	// The dropped flow's late response must not resurrect a half row.
	first.Status = 200
	if ok, err := s.EnqueueUpdateFlow(first); err != nil || !ok {
		t.Fatalf("enqueue update for dropped flow: ok=%v err=%v", ok, err)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	resumeFlusher(s)
	got, err := s.GetFlow(committed.ID)
	if err != nil || got.Status != 200 {
		t.Fatalf("committed flow lost its response update: %+v err=%v", got, err)
	}
	if _, err := s.GetFlow(first.ID); err == nil {
		t.Fatalf("dropped flow %d was persisted", first.ID)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM flows WHERE status=0 AND path NOT LIKE '/fill-%'`); n != 0 {
		t.Fatalf("%d rows left pending (status 0)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM flows`); n != flowBufCap {
		t.Fatalf("persisted %d flows, want %d", n, flowBufCap)
	}
	st := s.CaptureStats()
	if st.FlowWritesDropped != 1 || !st.Lossy() || st.FlowQueue != 0 || st.FlowQueueCap != flowBufCap {
		t.Fatalf("CaptureStats = %+v, want 1 dropped write, empty queue", st)
	}
}
