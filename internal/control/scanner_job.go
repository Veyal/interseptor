package control

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

// A passive scan is a blocking handler, so the only way to stop one is from a
// second request; scanJob is the registry that lets POST /api/scanner/cancel
// reach a scan in flight.
//
// Cancel and the handler's persist step race on one per-scan atomic state, so
// exactly one wins. A scan starts `scanRunning`. The handler, the instant
// before it writes anything to the store, tries to move it to `scanCommitting`;
// a cancel tries to move it to `scanCancelled`. Both transitions require the
// state to still be `scanRunning`, so a scan can never both persist and be
// reported cancelled — which is the invariant the old `len(running)` count got
// wrong, reporting `cancelled: 1` for a scan whose workers had already finished
// and which went on to persist and return 200.
type scanState int32

const (
	scanRunning    scanState = iota // workers active or finishing; still interruptible
	scanCancelled                   // a cancel claimed it; the handler must abort
	scanCommitting                  // past the point of no return; persisting
)

// scanHandle is one registered scan.
type scanHandle struct {
	job    *scanJob
	id     int64
	cancel context.CancelFunc
	state  atomic.Int32
}

type scanJob struct {
	mu      sync.Mutex
	nextID  int64
	running map[int64]*scanHandle
}

// begin registers a scan and returns its context and handle. The caller must
// call handle.release() when the scan finishes, however it finishes.
func (j *scanJob) begin(parent context.Context) (context.Context, *scanHandle) {
	ctx, cancel := context.WithCancel(parent)
	h := &scanHandle{job: j, cancel: cancel}
	h.state.Store(int32(scanRunning))
	j.mu.Lock()
	if j.running == nil {
		j.running = map[int64]*scanHandle{}
	}
	j.nextID++
	h.id = j.nextID
	j.running[h.id] = h
	j.mu.Unlock()
	return ctx, h
}

// claimCommit tries to move the scan past the point of no return. It returns
// true when the caller may persist; false means a cancel got there first, so
// the caller must persist nothing. Called once, immediately before the first
// write to the store.
func (h *scanHandle) claimCommit() bool {
	return h.state.CompareAndSwap(int32(scanRunning), int32(scanCommitting))
}

// release deregisters the scan and releases its context. Safe to call in any
// terminal state.
func (h *scanHandle) release() {
	h.job.mu.Lock()
	delete(h.job.running, h.id)
	h.job.mu.Unlock()
	h.cancel()
}

// cancelAll stops every scan still in its interruptible phase and reports how
// many it actually interrupted. A scan that has already committed (or already
// been cancelled) is not counted: `cancelled: 1` is reserved for a scan that
// this call moved from running to cancelled, so a 0 means there was nothing
// left to stop. The context cancels run outside the lock so cancelling a scan
// cannot deadlock against one that is starting.
func (j *scanJob) cancelAll() int {
	j.mu.Lock()
	handles := make([]*scanHandle, 0, len(j.running))
	for _, h := range j.running {
		handles = append(handles, h)
	}
	j.mu.Unlock()
	n := 0
	for _, h := range handles {
		if h.state.CompareAndSwap(int32(scanRunning), int32(scanCancelled)) {
			h.cancel()
			n++
		}
	}
	return n
}

// count reports how many scans are registered, interruptible or not.
func (j *scanJob) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.running)
}

// scanProgressEvent is the `scan.progress` SSE payload. cancelled distinguishes
// a scan that was stopped from one that finished, which done alone cannot.
func scanProgressEvent(p scanner.Progress, cancelled bool) map[string]any {
	return map[string]any{
		"type": "scan.progress",
		"scan": map[string]any{
			"scanned":   p.Scanned,
			"total":     p.Total,
			"issues":    p.Issues,
			"skipped":   p.Skipped,
			"done":      p.Done,
			"cancelled": cancelled,
		},
	}
}

// scanIssueEvent is the `scan.issue` SSE payload. saved is always false: the
// issue has not been through reconciliation yet, so it carries no id. The
// `scanner.update` event that follows the scan marks the persisted set, which
// GET /api/scanner/issues then returns with ids and in severity order.
func scanIssueEvent(is store.Issue) map[string]any {
	return map[string]any{"type": "scan.issue", "issue": is, "saved": false}
}
