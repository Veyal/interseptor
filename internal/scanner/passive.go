package scanner

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"

	"github.com/Veyal/interseptor/internal/store"
)

// This file is the passive scan engine: it fans the per-flow work of Analyze
// out across goroutines, keeps the result in flow order, streams progress, and
// stops when its context is cancelled.
//
// It owns the loop but not the I/O or the custom-check runtime: bodies arrive
// through a caller-supplied function, and Starlark checks through another, so
// scanner stays a pure package over store.Flow and does not import checkscript
// or the control plane. That keeps the dependency direction one-way.

// maxStreamedIssues caps how many issues are handed to OnIssue. A scan over
// 5000 flows can produce thousands; past this point the live stream stops and
// the running counts in Progress carry the rest. The returned Result is always
// complete.
const maxStreamedIssues = 200

// progressReports is roughly how many Progress callbacks a whole scan makes.
// One per flow would be 5000 SSE frames for a full scan.
const progressReports = 50

// Progress is one report of how far a passive scan has got. Scanned counts
// flows the engine has finished with — including ones skipped as out of scope
// or with a missing body blob — so it always reaches Total.
type Progress struct {
	Scanned int  `json:"scanned"`
	Total   int  `json:"total"`
	Issues  int  `json:"issues"`
	Skipped int  `json:"skipped"`
	Done    bool `json:"done"`
}

// BodyLoader returns a flow's request and response bodies. A wrapped
// os.ErrNotExist skips that one flow; any other error fails the scan, because
// a disk or database fault would otherwise silently shrink the results.
type BodyLoader func(f *store.Flow) (reqBody, resBody []byte, err error)

// CustomChecks runs the caller's user-authored checks over one flow. The
// control plane supplies this, wrapping internal/checkscript.
type CustomChecks func(f *store.Flow, reqBody, resBody []byte) []store.Issue

// Request describes one passive scan.
type Request struct {
	// Flows are scanned in this order, and the results come back in it.
	Flows []*store.Flow
	// Workers is the goroutine count. 0 means runtime.NumCPU().
	Workers int
	// Disabled holds built-in check ids to skip. nil runs every built-in.
	Disabled map[string]bool
	// Bodies loads each flow's bodies. Required.
	Bodies BodyLoader
	// InScope filters which flows are analysed. nil analyses every flow.
	// Flows it rejects still appear in Result.FlowIDs, because the caller
	// reconciles stored issues against the whole queried set.
	InScope func(f *store.Flow) bool
	// Custom runs user-authored checks. nil runs only the built-ins.
	Custom CustomChecks
	// OnProgress and OnIssue stream the scan. Both are called from one
	// goroutine, in flow order, so a consumer needs no locking of its own.
	OnProgress func(Progress)
	OnIssue    func(store.Issue)
}

// Result is the outcome of a completed passive scan.
type Result struct {
	// Issues in flow order, then in each flow's own check order.
	Issues []store.Issue
	// FlowIDs is every flow the scan considered, in input order — the
	// reconciliation universe, including skipped and out-of-scope flows.
	FlowIDs []int64
	// Skipped counts flows whose body blobs were missing.
	Skipped int
}

// workerCount resolves how many goroutines to run: the request's own count,
// else one per CPU, never more than there are flows and never less than one.
func workerCount(want, flows int) int {
	if want <= 0 {
		want = runtime.NumCPU()
	}
	if want > flows {
		want = flows
	}
	if want < 1 {
		want = 1
	}
	return want
}

// RunPassive scans every flow in req, in parallel, and returns the issues in
// flow order. It returns ctx.Err() if the scan is cancelled, and the first body
// error that is not a missing blob.
func RunPassive(ctx context.Context, req Request) (Result, error) {
	total := len(req.Flows)
	if total == 0 {
		report(req.OnProgress, Progress{Done: true})
		return Result{}, ctx.Err()
	}

	flowIDs := make([]int64, total)
	for i, f := range req.Flows {
		flowIDs[i] = f.ID
	}
	// Each worker writes only its own slot, so the per-flow results need no
	// lock; the collector reads a slot only after its done signal arrives.
	perFlow := make([][]store.Issue, total)
	skipped := make([]bool, total)

	done := make(chan int, total)
	var firstErr error
	var errOnce sync.Once
	scanCtx, stop := context.WithCancel(ctx)
	defer stop()

	var next int
	var dispatch sync.Mutex
	take := func() (int, bool) {
		dispatch.Lock()
		defer dispatch.Unlock()
		if next >= total {
			return 0, false
		}
		i := next
		next++
		return i, true
	}

	var wg sync.WaitGroup
	for w := 0; w < workerCount(req.Workers, total); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i, ok := take()
				if !ok {
					return
				}
				if scanCtx.Err() != nil {
					return
				}
				issues, skip, err := scanOne(req, req.Flows[i])
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					stop() // one real I/O fault ends the scan
					return
				}
				perFlow[i] = issues
				skipped[i] = skip
				done <- i
			}
		}()
	}

	collected := make(chan Result, 1)
	go func() { collected <- collect(req, done, perFlow, skipped, flowIDs) }()

	wg.Wait()
	close(done)
	res := <-collected

	if firstErr != nil {
		return Result{}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return res, nil
}

// scanOne analyses a single flow: load its bodies, run the built-ins, then the
// custom checks. skip is true when the flow's body blobs are missing.
func scanOne(req Request, f *store.Flow) (issues []store.Issue, skip bool, err error) {
	if req.InScope != nil && !req.InScope(f) {
		return nil, false, nil
	}
	reqBody, resBody, err := req.Bodies(f)
	if err != nil {
		// A missing blob (externally deleted file, partial project copy) skips
		// this flow: one broken blob must not discard a whole scan's results.
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, err
	}
	issues = AnalyzeWithDisabled(f, reqBody, resBody, req.Disabled)
	if req.Custom != nil {
		issues = append(issues, req.Custom(f, reqBody, resBody)...)
	}
	return issues, false, nil
}

// collect drains the workers' done signals and assembles the ordered result.
// It flushes contiguously from the front, so OnIssue sees issues in flow order
// even though the workers finish out of order.
func collect(req Request, done <-chan int, perFlow [][]store.Issue, skipped []bool, flowIDs []int64) Result {
	total := len(perFlow)
	complete := make([]bool, total)
	var flushed, finished, issueCount, skippedCount, streamed int
	var out []store.Issue

	step := total / progressReports
	if step < 1 {
		step = 1
	}
	for i := range done {
		complete[i] = true
		finished++
		// Flush every flow whose turn has come.
		for flushed < total && complete[flushed] {
			if skipped[flushed] {
				skippedCount++
			}
			for _, is := range perFlow[flushed] {
				out = append(out, is)
				issueCount++
				if req.OnIssue != nil && streamed < maxStreamedIssues {
					streamed++
					req.OnIssue(is)
				}
			}
			flushed++
		}
		if finished%step == 0 {
			report(req.OnProgress, Progress{Scanned: finished, Total: total, Issues: issueCount, Skipped: skippedCount})
		}
	}
	report(req.OnProgress, Progress{Scanned: finished, Total: total, Issues: issueCount, Skipped: skippedCount, Done: true})
	return Result{Issues: out, FlowIDs: flowIDs[:flushed], Skipped: skippedCount}
}

func report(fn func(Progress), p Progress) {
	if fn != nil {
		fn(p)
	}
}
