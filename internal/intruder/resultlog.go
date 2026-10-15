package intruder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// resultLog is an append-only store of attack results. It keeps memory bounded
// by flushing full chunks to disk (one JSON file per chunk) while retaining a
// small in-memory head window for cheap live polling, so a run of tens of
// thousands of requests never holds every result in RAM at once.
//
// Results are stored raw. Flagged/Anomaly are derived at read time from the
// run's finalised anomaly statistics, so finishing a run never rewrites a chunk.
// When dir is empty (temp-dir creation failed) the log degrades to memory-only:
// correctness is preserved, only the memory bound is lost.
type resultLog struct {
	mu        sync.Mutex
	dir       string
	chunkSize int
	headCap   int

	n       int      // total appended
	flushed int      // number of chunk files on disk
	buf     []Result // current unflushed chunk (len <= chunkSize), or everything in memory mode
	head    []Result // first headCap results, kept for live snapshots

	acc       anomalyAcc
	finalized bool
	stat      flagStat
}

func newResultLog(dir string, chunkSize, headCap int) *resultLog {
	if chunkSize < 1 {
		chunkSize = 1000
	}
	if headCap < 1 {
		headCap = 1
	}
	return &resultLog{dir: dir, chunkSize: chunkSize, headCap: headCap}
}

// newRunLog makes a disk-backed log under a fresh temp dir, degrading to
// memory-only when the temp dir cannot be created.
func newRunLog(chunkSize, headCap int) *resultLog {
	dir, err := os.MkdirTemp("", "interseptor-intruder-*")
	if err != nil {
		dir = ""
	}
	return newResultLog(dir, chunkSize, headCap)
}

func (l *resultLog) append(r Result) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	if len(l.head) < l.headCap {
		l.head = append(l.head, r)
	}
	l.acc.add(r)
	l.buf = append(l.buf, r)
	// In memory mode buf holds everything; on disk flush full chunks out of RAM.
	if l.dir != "" && len(l.buf) >= l.chunkSize {
		l.flushLocked()
	}
}

func (l *resultLog) flushLocked() {
	if len(l.buf) == 0 {
		return
	}
	data, err := json.Marshal(l.buf)
	if err == nil {
		if werr := os.WriteFile(l.chunkPath(l.flushed), data, 0o600); werr == nil {
			l.flushed++
			l.buf = nil
			return
		}
	}
	// On any disk failure keep the chunk in memory rather than losing results;
	// future appends simply grow buf (the memory bound is best-effort).
}

func (l *resultLog) chunkPath(i int) string {
	return filepath.Join(l.dir, fmt.Sprintf("chunk-%06d.json", i))
}

func (l *resultLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// snapshotHead returns a copy of the live head window (no flags applied).
func (l *resultLog) snapshotHead() []Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Result(nil), l.head...)
}

// finalize computes the run's anomaly statistics so reads can derive flags.
func (l *resultLog) finalize() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stat = l.acc.stat()
	l.finalized = true
}

// readRange returns results [off, off+limit), applying derived flags once the
// run is finalised. A non-positive limit returns the whole range from off.
func (l *resultLog) readRange(off, limit int) []Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	if off < 0 {
		off = 0
	}
	end := l.n
	if limit > 0 && off+limit < end {
		end = off + limit
	}
	out := make([]Result, 0, max(0, end-off))
	idx := off
	for idx < end {
		chunk := idx / l.chunkSize
		data := l.chunkData(chunk)
		within := idx % l.chunkSize
		for within < len(data) && idx < end {
			r := data[within]
			if l.finalized {
				l.stat.apply(&r)
			}
			out = append(out, r)
			within++
			idx++
		}
		if within < len(data) && idx >= end {
			break
		}
		if len(data) == 0 {
			break // guard against a missing chunk so a read can never spin
		}
	}
	return out
}

// chunkData returns the results of chunk i: a flushed file, or the in-memory buf
// for the current (last) chunk. Caller holds l.mu.
func (l *resultLog) chunkData(i int) []Result {
	if i >= l.flushed {
		return l.buf
	}
	raw, err := os.ReadFile(l.chunkPath(i))
	if err != nil {
		return nil
	}
	var rs []Result
	if json.Unmarshal(raw, &rs) != nil {
		return nil
	}
	return rs
}

// all returns every result with flags applied (used for the finished snapshot
// and the persisted run record).
func (l *resultLog) all() []Result {
	return l.readRange(0, 0)
}

// cleanup removes the on-disk chunks.
func (l *resultLog) cleanup() {
	if l == nil {
		return
	}
	l.mu.Lock()
	dir := l.dir
	l.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// ---- anomaly statistics -----------------------------------------------------

// anomalyAcc accumulates the signals flagging needs without holding every
// result: modal status, the length distribution, and the grep-match majority.
type anomalyAcc struct {
	statusCount map[int]int
	lengths     []int64
	matched     int
	valid       int
}

// add folds one result in. Parse/transport failures (Status 0 or Error set) are
// excluded so they cannot skew the modal status or median length.
func (a *anomalyAcc) add(r Result) {
	if r.Status <= 0 || r.Error != "" {
		return
	}
	if a.statusCount == nil {
		a.statusCount = map[int]int{}
	}
	a.statusCount[r.Status]++
	a.lengths = append(a.lengths, r.Length)
	if r.Matched {
		a.matched++
	}
	a.valid++
}

func (a *anomalyAcc) stat() flagStat {
	if a.valid == 0 {
		return flagStat{}
	}
	mode, best := 0, -1
	for st, c := range a.statusCount {
		if c > best {
			best, mode = c, st
		}
	}
	med := median(a.lengths)
	tol := med / 5 // 20%
	if tol < 50 {
		tol = 50
	}
	return flagStat{
		modeStatus:       mode,
		medianLen:        med,
		tolerance:        tol,
		majorityMatched:  a.matched*2 > a.valid,
		grepSignalActive: a.matched > 0 && a.matched < a.valid,
		valids:           a.valid,
	}
}

// flagStat is the finalised thresholds used to flag a single result.
type flagStat struct {
	modeStatus       int
	medianLen        int64
	tolerance        int64
	majorityMatched  bool
	grepSignalActive bool
	valids           int
}

// apply sets Flagged/Anomaly on one result against the run's thresholds. It
// mirrors the original flagAnomalies logic: a length outlier is an Anomaly; a
// non-modal/5xx status, a length outlier or a minority grep verdict is Flagged.
func (fs flagStat) apply(r *Result) {
	if fs.valids == 0 || r.Status <= 0 || r.Error != "" {
		return
	}
	statusAnomaly := r.Status != fs.modeStatus || r.Status >= 500
	diff := r.Length - fs.medianLen
	if diff < 0 {
		diff = -diff
	}
	lengthAnomaly := diff > fs.tolerance
	grepAnomaly := fs.grepSignalActive && (r.Matched != fs.majorityMatched)
	if lengthAnomaly {
		r.Anomaly = true
	}
	if statusAnomaly || lengthAnomaly || grepAnomaly {
		r.Flagged = true
	}
}

// flagResults flags a slice in place by deriving thresholds from the slice
// itself (used by the anomaly unit tests and any in-memory caller).
func flagResults(rs []Result) {
	if len(rs) == 0 {
		return
	}
	var acc anomalyAcc
	for _, r := range rs {
		acc.add(r)
	}
	fs := acc.stat()
	for i := range rs {
		fs.apply(&rs[i])
	}
}
