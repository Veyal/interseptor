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

// TestFlusherGroupsTrickleIntoFewTransactions pins group commit: proxied flows
// arrive one at a time, and a flusher that drained on every wake-up committed a
// transaction per flow — too slow to keep up, so the buffer filled and capture
// dropped flows. A short linger lets a trickle share one transaction (and lets
// a fast response coalesce into its still-queued insert).
func TestFlusherGroupsTrickleIntoFewTransactions(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	const n = 200
	before := s.flowTxs.Load()
	for i := 0; i < n; i++ {
		queueInsert(t, s, "/trickle-"+strconv.Itoa(i))
		for start := time.Now(); time.Since(start) < 50*time.Microsecond; {
		}
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM flows`); got != n {
		t.Fatalf("persisted %d flows, want %d", got, n)
	}
	if txs := s.flowTxs.Load() - before; txs > n/8 {
		t.Fatalf("%d flows arriving %v apart used %d transactions, want at most %d", n, 50*time.Microsecond, txs, n/8)
	}
}

// fillWhileFlusherDelayed fills the buffer to capacity while the flusher sits in
// its apply delay, so the next enqueue finds it full.
func fillWhileFlusherDelayed(t *testing.T, s *Store, delay time.Duration) {
	t.Helper()
	flowApplyDelayNS.Store(int64(delay))
	t.Cleanup(func() { flowApplyDelayNS.Store(0) })
	for i := 0; i < flowBufCap; i++ {
		queueInsert(t, s, "/full-"+strconv.Itoa(i))
	}
	if n := s.pendingLen(); n != flowBufCap {
		t.Fatalf("buffer holds %d writes, want %d (flusher drained during fill)", n, flowBufCap)
	}
}

// TestFullBufferWaitsForFlusherInsteadOfDropping is the load-gate contract:
// when the proxy outruns the flusher, the next capture waits (bounded) for the
// flusher to take the buffer instead of discarding a flow, so saved flows equal
// requests sent.
func TestFullBufferWaitsForFlusherInsteadOfDropping(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	const delay = 300 * time.Millisecond
	fillWhileFlusherDelayed(t, s, delay)
	start := time.Now()
	extra := queueInsert(t, s, "/extra")
	waited := time.Since(start)
	flowApplyDelayNS.Store(0)
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	if n := s.FlowsDropped(); n != 0 {
		t.Fatalf("dropped %d writes, want 0", n)
	}
	if waited < 20*time.Millisecond {
		t.Fatalf("enqueue into a full buffer returned after %s without waiting", waited)
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM flows`); got != flowBufCap+1 {
		t.Fatalf("persisted %d flows, want %d", got, flowBufCap+1)
	}
	if _, err := s.GetFlow(extra.ID); err != nil {
		t.Fatalf("waiting flow not persisted: %v", err)
	}
}

// TestFullBufferWaitIsBounded keeps forwarding alive when the flusher is stuck
// (a long foreign transaction, a dead disk): the wait gives up after
// flowFullWait and the overflow is dropped and counted.
func TestFullBufferWaitIsBounded(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	prev := flowFullWait
	flowFullWait = 50 * time.Millisecond
	t.Cleanup(func() { flowFullWait = prev })
	fillWhileFlusherDelayed(t, s, time.Second)
	start := time.Now()
	queueInsert(t, s, "/overflow")
	waited := time.Since(start)
	if waited < flowFullWait || waited > 2*time.Second {
		t.Fatalf("full-buffer enqueue waited %s, want about %s", waited, flowFullWait)
	}
	if n := s.FlowsDropped(); n != 1 {
		t.Fatalf("dropped %d writes, want 1", n)
	}
	flowApplyDelayNS.Store(0)
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
