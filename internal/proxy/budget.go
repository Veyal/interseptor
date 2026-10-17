package proxy

import (
	"bytes"
	"errors"
	"io"
	"log"
	"sync/atomic"
	"time"
)

// defaultTransformBudget bounds the bytes buffered at once, across every
// in-flight request and response, for match-&-replace and the intercept holds.
// Each body is already capped at maxTransformBody; this caps the sum, so a
// burst of large responses under a body rule (or a pile of held responses)
// cannot exhaust memory. Bodies that do not fit stream through untransformed
// and unheld, exactly like bodies over the per-body cap.
const defaultTransformBudget = 256 << 20

// byteBudget is a process-wide, non-blocking byte semaphore.
type byteBudget struct {
	max  int64
	used atomic.Int64
}

func newByteBudget(max int64) *byteBudget { return &byteBudget{max: max} }

// tryAcquire reserves n bytes, or reports false without reserving anything
// when that would exceed the budget.
func (b *byteBudget) tryAcquire(n int64) bool {
	if n <= 0 {
		return true
	}
	for {
		cur := b.used.Load()
		if cur+n > b.max {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

func (b *byteBudget) release(n int64) {
	if n > 0 {
		b.used.Add(-n)
	}
}

// sharedTransformBudget is used by every Server that has no budget of its own.
var sharedTransformBudget = newByteBudget(defaultTransformBudget)

func (s *Server) transformBudget() *byteBudget {
	if s.budget != nil {
		return s.budget
	}
	return sharedTransformBudget
}

// errTransformBudget reports that buffering a body would exceed the budget.
var errTransformBudget = errors.New("transform buffer budget exhausted")

// readBodyWithinBudget reads up to limit+1 bytes of r into memory, reserving
// them from budget as it goes (sizeHint bytes up front, so a known
// Content-Length is allocated once instead of by repeated doubling). It
// returns the bytes read, the bytes reserved (the caller must release them),
// and an error when the read failed or the budget ran out; on error the
// returned bytes are what was consumed from r, for the caller to restore.
func readBodyWithinBudget(r io.Reader, limit, sizeHint int64, budget *byteBudget) ([]byte, int64, error) {
	sizeHint = max(0, min(sizeHint, limit)) // unknown length (-1): charge as read
	if !budget.tryAcquire(sizeHint) {
		return nil, 0, errTransformBudget
	}
	br := &budgetReader{r: io.LimitReader(r, limit+1), budget: budget, held: sizeHint, free: sizeHint}
	var buf bytes.Buffer
	buf.Grow(int(sizeHint) + bytes.MinRead)
	_, err := buf.ReadFrom(br)
	return buf.Bytes(), br.held, err
}

// budgetReader charges every byte read beyond the up-front reservation.
type budgetReader struct {
	r      io.Reader
	budget *byteBudget
	held   int64 // bytes reserved in total
	free   int64 // reserved bytes not yet consumed
}

func (b *budgetReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 {
		need := int64(n) - b.free
		if need <= 0 {
			b.free -= int64(n)
		} else {
			b.free = 0
			if !b.budget.tryAcquire(need) {
				return n, errTransformBudget
			}
			b.held += need
		}
	}
	return n, err
}

var budgetSkipLog atomic.Int64 // unix seconds of the last over-budget log line

// noteOverBudget logs (at most every 10s) that a body was forwarded without
// rules or hold because the transform budget was exhausted.
func noteOverBudget(b *byteBudget, what, host, path string) {
	now := time.Now().Unix()
	last := budgetSkipLog.Load()
	if now-last < 10 || !budgetSkipLog.CompareAndSwap(last, now) {
		return
	}
	log.Printf("proxy: %s for %s%s forwarded without match-&-replace or intercept hold: the %d MB transform buffer budget is in use by other in-flight bodies", what, host, path, b.max>>20)
}
