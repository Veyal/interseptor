package store

import (
	"database/sql"
	"errors"
	"log"
	"sync/atomic"
	"time"
)

// Flow capture from the proxy is write-behind: enqueue never waits on SQLite.
// One flusher applies a FIFO buffer so a flow's insert lands before its update
// and before a tag write for that same id. Direct InsertFlow / UpdateFlow
// callers stay synchronous; reads and later mutations wait only when they
// touch an id that is still queued.

const flowBufCap = 4096

// flowBatchMax bounds how many queued writes share one SQLite transaction. The
// batch is the point — one commit for the chunk instead of one per write — and
// the bound keeps both the write-lock hold time and the blast radius of a
// transaction that cannot commit finite when a burst has filled the buffer.
const flowBatchMax = 512

// flowDrainTimeout bounds how long Close waits for the queue to empty.
// flowApplyDelayNS slows the flusher so tests can enqueue while a write is
// in progress. flowBatchFault makes the batch transaction fail so tests can
// exercise the per-write fallback. All three are zero-cost in production (the
// delay is 0 and no fault is injected).
var (
	flowDrainTimeout = 5 * time.Second
	flowApplyDelayNS atomic.Int64
	flowBatchFault   atomic.Int32
)

const (
	faultFlowBatchBegin  int32 = 1
	faultFlowBatchCommit int32 = 2
)

var (
	errFlowsClosed      = errors.New("flow capture closed")
	errFlowDrainTimeout = errors.New("flow write-behind drain timed out")
	// errFlowOpSkipped marks a queued write the flusher deliberately discards:
	// an update for a row that was never inserted, or a tag write for a flow
	// that is gone. Both are detected before anything is written, so the chunk
	// carries on; the error is logged rather than surfaced as a persistence
	// failure or treated as a reason to replay.
	errFlowOpSkipped = errors.New("flow write skipped")
)

type flowOpKind uint8

const (
	flowOpInsert flowOpKind = iota
	flowOpUpdate
	flowOpAddTags
)

type flowOp struct {
	kind flowOpKind
	flow *Flow
	id   int64
	tags []string
	// release holds the body hashes of snapshots this op replaced in place.
	// Each finalized body carries one GC claim, released by the write that
	// queued its hash; folding two writes into one must still release both
	// sets, and only once the surviving write has committed.
	release []string
}

func (op flowOp) bodyHashes() []string {
	out := op.release
	if f := op.flow; f != nil {
		out = append(out[:len(out):len(out)], f.ReqBodyHash, f.ResBodyHash, f.OriginalReqBodyHash, f.OriginalResBodyHash)
	}
	return out
}

type flowCmd struct {
	close bool
	errc  chan error
}

// EnqueueInsertFlow assigns an id and queues a snapshot of f. It never waits
// on SQLite. On a full buffer the oldest queued write is dropped and counted.
// The returned bool is false only when the queue is closed.
func (s *Store) EnqueueInsertFlow(f *Flow) (bool, error) {
	if f == nil {
		return false, errors.New("nil flow")
	}
	s.flowMu.Lock()
	if s.flowClosed || s.flowWake == nil {
		s.flowMu.Unlock()
		return false, errFlowsClosed
	}
	dropped := s.dropOldestFlowLocked()
	if f.ID == 0 {
		f.ID = s.allocFlowID()
	} else {
		s.noteFlowID(f.ID)
	}
	cp := cloneFlow(f)
	s.appendFlowOpLocked(flowOp{kind: flowOpInsert, flow: cp, id: cp.ID})
	s.wakeFlowLocked()
	s.flowMu.Unlock()
	s.publishBodies(dropped.bodyHashes()...)
	return true, nil
}

// EnqueueUpdateFlow queues a snapshot of f, keyed by f.ID. It never waits on
// SQLite. When the flow's insert (or an earlier update) is still queued, the
// snapshot replaces it in place: one SQLite write per flow instead of two, and
// no extra slot in a full buffer. Otherwise a full buffer drops a write first
// (see dropOldestFlowLocked). An update whose insert was already dropped is
// discarded and its body protection is released.
func (s *Store) EnqueueUpdateFlow(f *Flow) (bool, error) {
	if f == nil || f.ID == 0 {
		return false, errors.New("update requires a flow id")
	}
	s.flowMu.Lock()
	if s.flowClosed || s.flowWake == nil {
		s.flowMu.Unlock()
		return false, errFlowsClosed
	}
	if _, void := s.flowVoid[f.ID]; void && s.flowPendingN[f.ID] == 0 {
		s.flowMu.Unlock()
		s.publishFlowBodies(f)
		return true, nil
	}
	if op := s.queuedSnapshotLocked(f.ID); op != nil {
		op.release = op.bodyHashes()
		op.flow = cloneFlow(f)
		s.flowMu.Unlock()
		return true, nil
	}
	dropped := s.dropOldestFlowLocked()
	cp := cloneFlow(f)
	s.appendFlowOpLocked(flowOp{kind: flowOpUpdate, flow: cp, id: cp.ID})
	s.wakeFlowLocked()
	s.flowMu.Unlock()
	s.publishBodies(dropped.bodyHashes()...)
	return true, nil
}

// appendFlowOpLocked queues op and records it as the newest snapshot of its
// flow when it carries one.
func (s *Store) appendFlowOpLocked(op flowOp) {
	seq := s.flowBase + uint64(len(s.flowPending))
	s.flowPending = append(s.flowPending, op)
	s.bumpFlowPendingLocked(op.id)
	if op.kind == flowOpInsert || op.kind == flowOpUpdate {
		if s.flowLatest == nil {
			s.flowLatest = map[int64]uint64{}
		}
		s.flowLatest[op.id] = seq
	}
}

// queuedSnapshotLocked returns the still-queued insert or update that holds
// id's newest snapshot, or nil when none is waiting (the flusher has taken it).
func (s *Store) queuedSnapshotLocked(id int64) *flowOp {
	seq, ok := s.flowLatest[id]
	if !ok || seq < s.flowBase {
		return nil
	}
	i := seq - s.flowBase
	if i >= uint64(len(s.flowPending)) {
		return nil
	}
	op := &s.flowPending[i]
	if op.id != id || (op.kind != flowOpInsert && op.kind != flowOpUpdate) {
		return nil
	}
	return op
}

// takeFlowPendingLocked hands the whole buffer to the caller and forgets the
// in-place snapshot index: nothing taken may be modified any more.
func (s *Store) takeFlowPendingLocked() []flowOp {
	batch := s.flowPending
	s.flowPending = nil
	s.flowBase += uint64(len(batch))
	clear(s.flowLatest)
	return batch
}

// EnqueueAddFlowTags queues a tag union for id. It never waits on SQLite.
func (s *Store) EnqueueAddFlowTags(id int64, tags []string) (bool, error) {
	tags = NormalizeTags(tags)
	if id == 0 || len(tags) == 0 {
		return true, nil
	}
	s.flowMu.Lock()
	if s.flowClosed || s.flowWake == nil {
		s.flowMu.Unlock()
		return false, errFlowsClosed
	}
	if _, void := s.flowVoid[id]; void && s.flowPendingN[id] == 0 {
		s.flowMu.Unlock()
		return true, nil
	}
	dropped := s.dropOldestFlowLocked()
	cp := append([]string(nil), tags...)
	s.appendFlowOpLocked(flowOp{kind: flowOpAddTags, id: id, tags: cp})
	s.wakeFlowLocked()
	s.flowMu.Unlock()
	s.publishBodies(dropped.bodyHashes()...)
	return true, nil
}

// AddFlowTagsNonBlocking tags id without waiting when that id still has queued
// writes (the proxy capture path). A flow with nothing queued is written
// immediately so repeater sends and tests stay synchronous.
func (s *Store) AddFlowTagsNonBlocking(id int64, tags []string) {
	if id == 0 || len(tags) == 0 {
		return
	}
	if s.flowBusyID(id) {
		_, _ = s.EnqueueAddFlowTags(id, tags)
		return
	}
	_, _ = s.AddFlowTags(id, tags)
}

// FlushFlows blocks until writes queued before this call have been applied
// (or the flusher stops). Tests and read paths use it to observe capture.
func (s *Store) FlushFlows() error {
	s.flowCmdMu.Lock()
	defer s.flowCmdMu.Unlock()
	s.flowMu.Lock()
	closed := s.flowClosed
	ready := s.flowOpCh != nil
	s.flowMu.Unlock()
	if closed || !ready {
		return errFlowsClosed
	}
	errc := make(chan error, 1)
	s.flowOpCh <- flowCmd{errc: errc}
	return <-errc
}

// FlowsDropped reports how many queued flow writes were discarded because the
// buffer was full or shutdown gave up. Capture stays non-blocking.
func (s *Store) FlowsDropped() uint64 {
	return s.flowDropped.Load()
}

// CaptureStats is the capture pipeline's loss and backlog since this store was
// opened. Non-zero drop counters mean History is missing traffic that went
// through the proxy, so every operator-facing status surface reports them.
type CaptureStats struct {
	FlowWritesDropped uint64 `json:"flowWritesDropped"`
	WSFramesDropped   uint64 `json:"wsFramesDropped"`
	FlowQueue         int    `json:"flowQueue"`
	FlowQueueCap      int    `json:"flowQueueCap"`
}

// Lossy reports whether any captured flow write or WebSocket frame was dropped.
func (c CaptureStats) Lossy() bool { return c.FlowWritesDropped > 0 || c.WSFramesDropped > 0 }

// CaptureStats snapshots the capture drop counters and the flow queue depth.
func (s *Store) CaptureStats() CaptureStats {
	return CaptureStats{
		FlowWritesDropped: s.flowDropped.Load(),
		WSFramesDropped:   s.wsDropped.Load(),
		FlowQueue:         s.pendingLen(),
		FlowQueueCap:      flowBufCap,
	}
}

func (s *Store) allocFlowID() int64 {
	return s.nextFlowID.Add(1)
}

func (s *Store) noteFlowID(id int64) {
	for {
		cur := s.nextFlowID.Load()
		if id <= cur {
			return
		}
		if s.nextFlowID.CompareAndSwap(cur, id) {
			return
		}
	}
}

func (s *Store) startFlowFlusher() {
	s.flowPendingN = map[int64]int{}
	s.flowVoid = map[int64]struct{}{}
	s.flowWake = make(chan struct{}, 1)
	s.flowOpCh = make(chan flowCmd, 1)
	s.flowStop = make(chan struct{})
	s.flowDone = make(chan struct{})
	go s.flowFlusher()
}

func (s *Store) shutdownFlows() error {
	var err error
	s.flowStopOnce.Do(func() {
		s.flowCmdMu.Lock()
		defer s.flowCmdMu.Unlock()
		if s.flowDone == nil {
			s.flowMu.Lock()
			s.flowClosed = true
			s.flowMu.Unlock()
			return
		}
		s.flowMu.Lock()
		s.flowClosed = true
		s.flowMu.Unlock()
		errc := make(chan error, 1)
		s.flowOpCh <- flowCmd{close: true, errc: errc}
		timer := time.NewTimer(flowDrainTimeout)
		select {
		case err = <-errc:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			<-s.flowDone
			if n := s.flowDropped.Load(); n > 0 {
				log.Printf("store: flow write-behind stopped, dropped %d", n)
			}
		case <-timer.C:
			s.flowGiveUp.Store(true)
			s.cancelFlowWait()
			s.abandonPending()
			err = errFlowDrainTimeout
			log.Printf("store: flow write-behind drain exceeded %s, dropped %d", flowDrainTimeout, s.flowDropped.Load())
		}
	})
	return err
}

func (s *Store) waitFlowExit() {
	if s.flowDone == nil {
		return
	}
	select {
	case <-s.flowDone:
	case <-time.After(2 * time.Second):
		log.Printf("store: flow write-behind flusher did not exit")
	}
}

func (s *Store) cancelFlowWait() {
	s.flowCancelOnce.Do(func() {
		if s.flowStop != nil {
			close(s.flowStop)
		}
	})
}

func (s *Store) flowFlusher() {
	defer close(s.flowDone)
	for {
		select {
		case <-s.flowWake:
			if s.flowIsPaused() {
				continue
			}
			_ = s.drainUntilIdle(time.Time{})
		case cmd := <-s.flowOpCh:
			var deadline time.Time
			if cmd.close {
				deadline = time.Now().Add(flowDrainTimeout)
			}
			cmd.errc <- s.drainUntilIdle(deadline)
			if cmd.close {
				return
			}
		}
	}
}

// drainUntilIdle applies queued writes until the buffer is empty, the pass
// cap is hit (live FlushFlows must not spin for a whole traffic burst), or
// deadline passes (Close).
func (s *Store) drainUntilIdle(deadline time.Time) error {
	bounded := !deadline.IsZero()
	var first error
	for pass := 0; ; pass++ {
		if s.flowGiveUp.Load() || (bounded && time.Now().After(deadline)) {
			s.abandonPending()
			if first == nil {
				first = errFlowDrainTimeout
			}
			return first
		}
		if s.pendingLen() == 0 {
			return first
		}
		if err := s.drainOnce(); err != nil && first == nil {
			first = err
		}
		if !bounded && pass >= 7 {
			return first
		}
	}
}

func (s *Store) drainOnce() error {
	if s.flowGiveUp.Load() {
		s.abandonPending()
		return errFlowDrainTimeout
	}
	s.pauseApply()
	if s.flowGiveUp.Load() {
		s.abandonPending()
		return errFlowDrainTimeout
	}
	s.flowMu.Lock()
	batch := s.takeFlowPendingLocked()
	if len(batch) > 0 {
		s.flowInFlight = true
	}
	s.flowMu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	var first error
	done := 0
	for done < len(batch) {
		if s.flowGiveUp.Load() {
			break
		}
		end := done + flowBatchMax
		if end > len(batch) {
			end = len(batch)
		}
		if err := s.applyFlowBatch(batch[done:end]); err != nil && first == nil {
			first = err
			log.Printf("store: persist flow: %v", err)
		}
		done = end
	}
	if done < len(batch) {
		s.abandonOps(batch[done:])
	}
	s.flowMu.Lock()
	s.flowInFlight = false
	s.flowMu.Unlock()
	return first
}

// applyFlowBatch applies one chunk of queued writes in a single SQLite
// transaction.
//
// Failure policy: commit the chunk whole, or replay it write by write. The
// chunk is applied optimistically with no per-write savepoint, because one
// queued write is several statements (row, full-text entry, tags) and a write
// that failed halfway would otherwise leave a flow row with no index entry. So
// the first error abandons the whole transaction — which commits nothing — and
// the chunk is replayed one write per transaction, the pre-batching path: every
// good write still lands, the bad one fails alone, nothing is half-applied, and
// the flusher carries on. A chunk that hits an error pays one wasted pass; the
// alternative, a savepoint around every write, cost twice the write time on
// every chunk whether or not anything failed (measured on an M4: 46µs against
// 23µs per write), because SQLite keeps a sub-journal while a savepoint is open.
//
// The same replay covers a transaction that cannot be opened or cannot commit.
//
// Body ownership and pending-id bookkeeping are released only once the chunk is
// committed, so body GC can never see a body as unowned while the row that
// references it is still uncommitted.
func (s *Store) applyFlowBatch(batch []flowOp) error {
	if s.applyFlowBatchTx(batch) {
		s.completeOps(batch)
		return nil
	}
	return s.applyFlowOpsOneByOne(batch)
}

// applyFlowBatchTx reports whether the whole chunk committed. A false return
// means the transaction wrote nothing, so the caller may replay the chunk
// without risking a duplicate row.
func (s *Store) applyFlowBatchTx(batch []flowOp) bool {
	if flowBatchFault.Load() == faultFlowBatchBegin {
		return false
	}
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("store: flow batch begin: %v", err)
		return false
	}
	s.flowTxs.Add(1)
	defer tx.Rollback()
	ex := &txStmts{tx: tx}
	for _, op := range batch {
		if s.flowIsVoid(op.id) {
			continue // the insert this write belongs to was dropped
		}
		if err := s.applyFlowOpTx(ex, op); err != nil {
			if errors.Is(err, errFlowOpSkipped) {
				continue
			}
			log.Printf("store: flow batch write %d, replaying chunk one write at a time: %v", op.id, err)
			return false
		}
	}
	if flowBatchFault.Load() == faultFlowBatchCommit {
		return false
	}
	if err := tx.Commit(); err != nil {
		log.Printf("store: flow batch commit: %v", err)
		return false
	}
	return true
}

// txStmts runs the flow writers' SQL inside one batch transaction, preparing
// each distinct statement once per batch instead of once per write. A chunk
// repeats the same three or four statements up to flowBatchMax times, and
// re-parsing them was about a third of the flusher's cost. The statements are
// owned by the transaction and closed when it commits or rolls back.
type txStmts struct {
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
}

func (t *txStmts) stmt(query string) (*sql.Stmt, error) {
	if st, ok := t.stmts[query]; ok {
		return st, nil
	}
	st, err := t.tx.Prepare(query)
	if err != nil {
		return nil, err
	}
	if t.stmts == nil {
		t.stmts = map[string]*sql.Stmt{}
	}
	t.stmts[query] = st
	return st, nil
}

func (t *txStmts) Exec(query string, args ...any) (sql.Result, error) {
	st, err := t.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Exec(args...)
}

func (t *txStmts) Query(query string, args ...any) (*sql.Rows, error) {
	st, err := t.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Query(args...)
}

func (t *txStmts) QueryRow(query string, args ...any) *sql.Row {
	st, err := t.stmt(query)
	if err != nil {
		return t.tx.QueryRow(query, args...) // carries the same error to Scan
	}
	return st.QueryRow(args...)
}

// applyFlowOpTx applies one queued write inside the caller's transaction.
func (s *Store) applyFlowOpTx(ex flowExec, op flowOp) error {
	switch op.kind {
	case flowOpInsert:
		return s.insertFlowExec(ex, op.flow)
	case flowOpUpdate:
		if err := writeFlowUpdateExec(ex, op.flow); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Printf("store: flow update %d skipped, row was not inserted", op.id)
				return errFlowOpSkipped
			}
			return err
		}
		return nil
	case flowOpAddTags:
		if _, err := writeFlowTagsExec(ex, op.id, op.tags); err != nil {
			// ErrNoRows can only come from the "does this flow still exist?"
			// check, which runs before any tag is written, so the transaction is
			// untouched and the write is simply dropped. Anything else may have
			// written part of the tag set, so it fails the chunk into the replay.
			if errors.Is(err, sql.ErrNoRows) {
				log.Printf("store: flow tags %d skipped, row is gone", op.id)
				return errFlowOpSkipped
			}
			return err
		}
		return nil
	default:
		return nil
	}
}

// insertFlowExec writes one queued flow. EnqueueInsertFlow always assigns the
// id before queueing; an id it did not set is allocated here so the function is
// total.
func (s *Store) insertFlowExec(ex flowExec, f *Flow) error {
	if f == nil {
		return nil
	}
	id := f.ID
	if id == 0 {
		id = s.allocFlowID()
	} else {
		s.noteFlowID(id)
	}
	if err := insertFlowRowExec(ex, f, nil, id); err != nil {
		return err
	}
	f.ID = id
	return nil
}

// applyFlowOpsOneByOne is the fallback for a batch transaction that wrote
// nothing: each queued write gets its own transaction, as it did before
// batching.
func (s *Store) applyFlowOpsOneByOne(batch []flowOp) error {
	var first error
	for i, op := range batch {
		if s.flowGiveUp.Load() {
			s.abandonOps(batch[i:])
			break
		}
		if err := s.applyFlowOp(op); err != nil && first == nil {
			first = err
			log.Printf("store: persist flow: %v", err)
		}
		s.completeOp(op)
		// applyFlowOp released op.flow's claims; the snapshots it replaced
		// in place still hold theirs.
		if len(op.release) > 0 {
			s.publishBodies(op.release...)
		}
	}
	return first
}

func (s *Store) applyFlowOp(op flowOp) error {
	if s.flowIsVoid(op.id) {
		s.publishFlowBodies(op.flow)
		return nil
	}
	switch op.kind {
	case flowOpInsert:
		_, err := s.insertFlow(op.flow, nil)
		return err
	case flowOpUpdate:
		err := s.writeFlowUpdate(op.flow)
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("store: flow update %d skipped, row was not inserted", op.id)
			return nil
		}
		return err
	case flowOpAddTags:
		if _, err := s.writeFlowTags(op.id, op.tags); err != nil {
			log.Printf("store: flow tags %d: %v", op.id, err)
		}
		return nil
	default:
		return nil
	}
}

func (s *Store) pauseApply() {
	d := flowApplyDelayNS.Load()
	if d <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(d))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.flowStop:
	}
}

// dropOldestFlowLocked makes room in a full buffer and returns the discarded
// write so the caller can release its body claims outside flowMu.
//
// The victim is the oldest queued insert, not simply the oldest write: an
// insert has not reached SQLite yet, so dropping it loses one whole flow (its
// later update and tags are discarded with it). Dropping the oldest write
// instead usually hit the response update of a row already committed, leaving
// that row stuck at status 0 ("pending") forever. Only a buffer holding no
// insert at all falls back to the oldest write.
func (s *Store) dropOldestFlowLocked() flowOp {
	if len(s.flowPending) < flowBufCap {
		return flowOp{}
	}
	victim := 0
	for i := range s.flowPending {
		if s.flowPending[i].kind == flowOpInsert {
			victim = i
			break
		}
	}
	op := s.flowPending[victim]
	if seq, ok := s.flowLatest[op.id]; ok && seq == s.flowBase+uint64(victim) {
		delete(s.flowLatest, op.id)
	}
	// Close the gap by shifting the writes ahead of the victim up one slot,
	// then trim the front. Those writes move one sequence number later, so
	// their snapshot index entries move with them.
	for i := victim; i > 0; i-- {
		prev := s.flowPending[i-1]
		if seq, ok := s.flowLatest[prev.id]; ok && seq == s.flowBase+uint64(i-1) {
			s.flowLatest[prev.id] = seq + 1
		}
		s.flowPending[i] = prev
	}
	s.flowPending[0] = flowOp{}
	s.flowPending = s.flowPending[1:]
	s.flowBase++
	n := s.flowDropped.Add(1)
	if n == 1 || n%1024 == 0 {
		log.Printf("store: flow buffer full, dropped %d write(s)", n)
	}
	s.finishFlowPendingLocked(op.id)
	if op.kind == flowOpInsert {
		if s.flowVoid == nil {
			s.flowVoid = map[int64]struct{}{}
		}
		s.flowVoid[op.id] = struct{}{}
	}
	return op
}

func (s *Store) bumpFlowPendingLocked(id int64) {
	if s.flowPendingN == nil {
		s.flowPendingN = map[int64]int{}
	}
	s.flowPendingN[id]++
}

func (s *Store) finishFlowPendingLocked(id int64) {
	n := s.flowPendingN[id]
	if n <= 1 {
		delete(s.flowPendingN, id)
		return
	}
	s.flowPendingN[id] = n - 1
}

func (s *Store) completeOp(op flowOp) {
	s.flowMu.Lock()
	s.finishFlowPendingLocked(op.id)
	if _, ok := s.flowPendingN[op.id]; !ok {
		delete(s.flowVoid, op.id)
	}
	s.flowMu.Unlock()
}

// completeOps retires a whole committed chunk: the pending-id bookkeeping for
// every write under one lock, then the body ownership those writes held. Both
// run after the commit, so a reader that sees an id as no longer queued finds
// its row, and body GC never sees a body as unowned before the row referencing
// it is durable.
func (s *Store) completeOps(batch []flowOp) {
	s.flowMu.Lock()
	for _, op := range batch {
		s.finishFlowPendingLocked(op.id)
		if _, ok := s.flowPendingN[op.id]; !ok {
			delete(s.flowVoid, op.id)
		}
	}
	s.flowMu.Unlock()
	// One bodyMu acquisition for the whole chunk. Capture takes that same lock
	// for every finalized body, so releasing per write made the flusher queue
	// behind the proxy up to flowBatchMax times per chunk — which is what
	// actually limited capture throughput once the commits were batched.
	s.bodyMu.Lock()
	for _, op := range batch {
		for _, sum := range op.release {
			s.releasePendingBody(sum)
		}
		if op.flow == nil {
			continue
		}
		s.releasePendingBody(op.flow.ReqBodyHash)
		s.releasePendingBody(op.flow.ResBodyHash)
		s.releasePendingBody(op.flow.OriginalReqBodyHash)
		s.releasePendingBody(op.flow.OriginalResBodyHash)
	}
	s.bodyMu.Unlock()
}

func (s *Store) abandonPending() int {
	s.flowMu.Lock()
	batch := s.takeFlowPendingLocked()
	s.flowInFlight = false
	for _, op := range batch {
		s.finishFlowPendingLocked(op.id)
		if op.kind == flowOpInsert {
			if s.flowVoid == nil {
				s.flowVoid = map[int64]struct{}{}
			}
			s.flowVoid[op.id] = struct{}{}
		}
	}
	s.flowMu.Unlock()
	return s.abandonOps(batch)
}

func (s *Store) abandonOps(batch []flowOp) int {
	for _, op := range batch {
		s.publishBodies(op.bodyHashes()...)
	}
	if len(batch) == 0 {
		return 0
	}
	s.flowDropped.Add(uint64(len(batch)))
	return len(batch)
}

func (s *Store) pendingLen() int {
	s.flowMu.Lock()
	n := len(s.flowPending)
	s.flowMu.Unlock()
	return n
}

func (s *Store) flowIsPaused() bool {
	s.flowMu.Lock()
	paused := s.flowPaused
	s.flowMu.Unlock()
	return paused
}

func (s *Store) flowIsVoid(id int64) bool {
	s.flowMu.Lock()
	_, ok := s.flowVoid[id]
	s.flowMu.Unlock()
	return ok
}

func (s *Store) flowBusyID(id int64) bool {
	s.flowMu.Lock()
	n := s.flowPendingN[id]
	s.flowMu.Unlock()
	return n > 0
}

func (s *Store) flowsBusy() bool {
	s.flowMu.Lock()
	busy := len(s.flowPending) > 0 || s.flowInFlight || len(s.flowPendingN) > 0
	s.flowMu.Unlock()
	return busy
}

func (s *Store) wakeFlowLocked() {
	select {
	case s.flowWake <- struct{}{}:
	default:
	}
}

// syncFlows drains the queue once when anything is pending so a list read
// sees rows the proxy has already handed to the UI.
func (s *Store) syncFlows() {
	if !s.flowsBusy() {
		return
	}
	_ = s.FlushFlows()
}

// waitFlow blocks until id has no queued writes, bounded so a stuck flusher
// cannot pin an API handler.
func (s *Store) waitFlow(id int64) {
	if id == 0 {
		return
	}
	for i := 0; i < 8; i++ {
		if !s.flowBusyID(id) {
			return
		}
		if err := s.FlushFlows(); err != nil {
			return
		}
	}
}

func (s *Store) publishFlowBodies(f *Flow) {
	if f == nil {
		return
	}
	s.publishBodies(f.ReqBodyHash, f.ResBodyHash, f.OriginalReqBodyHash, f.OriginalResBodyHash)
}

func cloneFlow(f *Flow) *Flow {
	if f == nil {
		return nil
	}
	cp := *f
	cp.ReqHeaders = cloneHeader(f.ReqHeaders)
	cp.ResHeaders = cloneHeader(f.ResHeaders)
	cp.OriginalReqHeaders = cloneHeader(f.OriginalReqHeaders)
	cp.OriginalResHeaders = cloneHeader(f.OriginalResHeaders)
	if f.Tags != nil {
		cp.Tags = append([]string(nil), f.Tags...)
	}
	return &cp
}

func cloneHeader(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
