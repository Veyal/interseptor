package store

import (
	"io"
	"strconv"
	"testing"
	"time"
)

// pauseFlusher stops the flusher goroutine from draining so a test owns the
// buffer and can call drainOnce itself.
func pauseFlusher(t *testing.T, s *Store) {
	t.Helper()
	s.flowMu.Lock()
	s.flowPaused = true
	s.flowMu.Unlock()
}

func resumeFlusher(s *Store) {
	s.flowMu.Lock()
	s.flowPaused = false
	s.flowMu.Unlock()
}

func queueInsert(t *testing.T, s *Store, path string) *Flow {
	t.Helper()
	f := &Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: path}
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue %s: ok=%v err=%v", path, ok, err)
	}
	return f
}

// TestFlowDrainAppliesBatchInOneTransaction pins the batching contract: a drain
// pass commits once for the whole buffer instead of once per queued write, and
// chunks at flowBatchMax so one transaction cannot hold the write lock for a
// whole 4,096-operation burst.
func TestFlowDrainAppliesBatchInOneTransaction(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)

	const n = 300
	for i := 0; i < n; i++ {
		queueInsert(t, s, "/batched-"+strconv.Itoa(i))
	}
	before := s.flowTxs.Load()
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if txs := s.flowTxs.Load() - before; txs != 1 {
		t.Fatalf("%d queued writes used %d transactions, want 1", n, txs)
	}
	if s.flowsBusy() {
		t.Fatalf("pending bookkeeping still busy after the batch committed")
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM flows`); got != n {
		t.Fatalf("persisted %d flows, want %d", got, n)
	}

	// A buffer larger than one chunk splits into one transaction per chunk.
	for i := 0; i < flowBatchMax+1; i++ {
		queueInsert(t, s, "/chunked-"+strconv.Itoa(i))
	}
	before = s.flowTxs.Load()
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce chunked: %v", err)
	}
	if txs := s.flowTxs.Load() - before; txs != 2 {
		t.Fatalf("%d queued writes used %d transactions, want 2", flowBatchMax+1, txs)
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM flows`); got != n+flowBatchMax+1 {
		t.Fatalf("persisted %d flows, want %d", got, n+flowBatchMax+1)
	}
}

// TestFlowDrainKeepsGoodRowsWhenOneOpFails is the point of batching: one bad
// write must not take the rest of the burst down with it. The failing operation
// rolls back to its own savepoint — row, full-text entry and tags together —
// and every other operation in the batch still commits.
func TestFlowDrainKeepsGoodRowsWhenOneOpFails(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// A row this store did not allocate, as if the headless CLI had written into
	// the same project. A queued insert for that id can only fail.
	taken := &Flow{ID: 4242, TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/taken"}
	if _, err := s.InsertFlow(taken); err != nil {
		t.Fatalf("InsertFlow taken id: %v", err)
	}

	pauseFlusher(t, s)
	first := queueInsert(t, s, "/before-the-failure")
	clash := &Flow{ID: 4242, TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com", Path: "/clash"}
	if ok, err := s.EnqueueInsertFlow(clash); err != nil || !ok {
		t.Fatalf("enqueue clash: ok=%v err=%v", ok, err)
	}
	after := queueInsert(t, s, "/after-the-failure")

	before := s.flowTxs.Load()
	if err := s.drainOnce(); err == nil {
		t.Fatalf("drainOnce reported success for a batch containing a failing write")
	}
	// The failing chunk was abandoned and replayed one write per transaction.
	if txs := s.flowTxs.Load() - before; txs < 4 {
		t.Fatalf("drain used %d transactions, want the abandoned chunk plus one per write", txs)
	}

	for _, f := range []*Flow{first, after} {
		got, err := s.GetFlow(f.ID)
		if err != nil {
			t.Fatalf("flow %s was lost to a mid-batch failure: %v", f.Path, err)
		}
		if got.Path != f.Path {
			t.Fatalf("flow %d path = %q, want %q", f.ID, got.Path, f.Path)
		}
	}
	// The failing insert changed nothing: the pre-existing row still holds its id.
	got, err := s.GetFlow(4242)
	if err != nil {
		t.Fatalf("GetFlow 4242: %v", err)
	}
	if got.Path != "/taken" {
		t.Fatalf("row 4242 path = %q, want /taken", got.Path)
	}
	// State is not corrupt: the rolled-back insert left no orphan full-text row.
	if flows, fts := countRows(t, s, `SELECT COUNT(*) FROM flows`), countRows(t, s, `SELECT COUNT(*) FROM flows_fts`); flows != fts {
		t.Fatalf("flows=%d flows_fts=%d, want equal counts", flows, fts)
	}
	if s.flowsBusy() {
		t.Fatalf("pending bookkeeping still busy after a failed write")
	}

	// The flusher is not wedged: it keeps applying writes afterwards.
	resumeFlusher(s)
	next := queueInsert(t, s, "/after-recovery")
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows after recovery: %v", err)
	}
	if _, err := s.GetFlow(next.ID); err != nil {
		t.Fatalf("flusher wedged after a failed write: %v", err)
	}
}

// TestFlowDrainReleasesBodiesOfFailedOp proves a write that fails inside the
// batch still hands its body ownership back. A leak there would fill
// pendingBodies and make capture start refusing bodies.
func TestFlowDrainReleasesBodiesOfFailedOp(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	taken := &Flow{ID: 777, TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/taken"}
	if _, err := s.InsertFlow(taken); err != nil {
		t.Fatalf("InsertFlow taken id: %v", err)
	}

	pauseFlusher(t, s)
	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, "body of a doomed insert"); err != nil {
		t.Fatalf("write body: %v", err)
	}
	hash, size, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	clash := &Flow{
		ID: 777, TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com",
		Path: "/clash", ResBodyHash: hash, ResLen: size,
	}
	if ok, err := s.EnqueueInsertFlow(clash); err != nil || !ok {
		t.Fatalf("enqueue clash: ok=%v err=%v", ok, err)
	}
	if err := s.drainOnce(); err == nil {
		t.Fatalf("drainOnce reported success for a failing write")
	}
	s.bodyMu.Lock()
	pending := len(s.pendingBodies)
	s.bodyMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d bodies still owned by the flusher after its write failed", pending)
	}
	// Nothing references the body now, so GC can reclaim it.
	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("GC removed %d bodies, want the orphan of the failed write", removed)
	}
}

// TestFlowDrainPublishesBodiesAfterBatchCommit pins the ordering body GC relies
// on: a queued flow keeps its body ownership until the batch transaction has
// committed, so GC can never see a body as unowned while the row referencing it
// is still uncommitted.
func TestFlowDrainPublishesBodiesAfterBatchCommit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	pauseFlusher(t, s)

	w, err := s.NewFlowBodyWriter()
	if err != nil {
		t.Fatalf("NewFlowBodyWriter: %v", err)
	}
	if _, err := io.WriteString(w, "queued body"); err != nil {
		t.Fatalf("write body: %v", err)
	}
	hash, size, err := w.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	f := &Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com",
		Path: "/queued-body", ResBodyHash: hash, ResLen: size,
	}
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	if removed, _, err := s.GCBodies(); err != nil || removed != 0 {
		t.Fatalf("GC removed %d bodies before the queued insert committed (err=%v)", removed, err)
	}
	if err := s.drainOnce(); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	s.bodyMu.Lock()
	pending := len(s.pendingBodies)
	s.bodyMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d bodies still owned by the flusher after the batch committed", pending)
	}
	if removed, _, err := s.GCBodies(); err != nil || removed != 0 {
		t.Fatalf("GC removed %d referenced bodies after the batch committed (err=%v)", removed, err)
	}
}

// TestFlowDrainFallsBackWhenBatchTransactionFails covers the other half of the
// failure policy. A batch transaction that cannot be opened or cannot commit
// commits nothing, so the whole batch is replayed one write per transaction
// rather than dropping up to flowBatchMax captured flows.
func TestFlowDrainFallsBackWhenBatchTransactionFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault int32
	}{
		{"begin", faultFlowBatchBegin},
		{"commit", faultFlowBatchCommit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			pauseFlusher(t, s)

			const n = 8
			queued := make([]*Flow, 0, n)
			for i := 0; i < n; i++ {
				queued = append(queued, queueInsert(t, s, "/fallback-"+strconv.Itoa(i)))
			}
			flowBatchFault.Store(tc.fault)
			t.Cleanup(func() { flowBatchFault.Store(0) })
			before := s.flowTxs.Load()
			if err := s.drainOnce(); err != nil {
				t.Fatalf("drainOnce: %v", err)
			}
			flowBatchFault.Store(0)

			for _, f := range queued {
				if _, err := s.GetFlow(f.ID); err != nil {
					t.Fatalf("flow %s lost when the batch transaction failed: %v", f.Path, err)
				}
			}
			if txs := s.flowTxs.Load() - before; txs < n {
				t.Fatalf("drain used %d transactions, want at least one per write (%d)", txs, n)
			}
			if s.flowsBusy() {
				t.Fatalf("pending bookkeeping still busy after the fallback")
			}
		})
	}
}

func countRows(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
