package collrun

import (
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
)

// Run statuses (ix_runs.status and Report.Status).
const (
	StatusRunning     = "running"
	StatusDone        = "done"
	StatusBailed      = "bailed"
	StatusStopped     = "stopped" // setNextRequest(null)
	StatusAborted     = "aborted"
	StatusLoopGuard   = "loop_guard"
	StatusNotApproved = "scripts_not_approved"
	StatusBadTarget   = "bad_next_request"
)

// VarChangeView is a variable write as shown in results: never the raw value.
type VarChangeView struct {
	Scope   string `json:"scope"`
	Key     string `json:"key"`
	Display string `json:"value,omitempty"`
	Unset   bool   `json:"unset,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
}

// ItemResult is one executed request of a run: the masked, bounded record that
// is persisted, streamed, reported and exported. It never carries a raw
// variable value or a response body (read the flow by FlowID).
type ItemResult struct {
	Seq         int                    `json:"seq"`
	Iteration   int                    `json:"iteration"` // 0-based
	ItemUID     string                 `json:"itemUid"`
	Name        string                 `json:"name"`
	Path        string                 `json:"path,omitempty"` // folder path, "A / B"
	Method      string                 `json:"method,omitempty"`
	URL         string                 `json:"url,omitempty"`
	Outcome     collexec.Outcome       `json:"outcome"`
	BlockReason collexec.BlockReason   `json:"blockReason,omitempty"`
	HTTPStatus  int                    `json:"status,omitempty"`
	StatusText  string                 `json:"statusText,omitempty"`
	DurationMs  int64                  `json:"durationMs,omitempty"`
	Size        int64                  `json:"size,omitempty"`
	FlowID      int64                  `json:"flowId,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Tests       []collexec.TestResult  `json:"tests,omitempty"`
	Console     []collexec.ConsoleLine `json:"console,omitempty"`
	VarChanges  []VarChangeView        `json:"varChanges,omitempty"`
	Scripts     []collexec.ScriptNote  `json:"scripts,omitempty"`
	Warnings    []string               `json:"warnings,omitempty"`
	Unresolved  []string               `json:"unresolved,omitempty"`
	NextRequest *string                `json:"nextRequest,omitempty"` // setNextRequest target ("" = stop)
}

// Problem reports whether the item should be offered for "rerun failed": a
// transport/script error, a block, or any test that did not pass or skip.
func (r ItemResult) Problem() bool {
	if r.Outcome == collexec.OutcomeError || r.Outcome == collexec.OutcomeBlocked {
		return true
	}
	for _, t := range r.Tests {
		switch t.Status {
		case collexec.TestFail, collexec.TestError, collexec.TestUnsupported:
			return true
		}
	}
	return false
}

// Totals aggregates a run.
type Totals struct {
	Requests    int `json:"requests"` // executed steps (every iteration)
	Sent        int `json:"sent"`
	Skipped     int `json:"skipped"`
	Blocked     int `json:"blocked"`
	Errors      int `json:"errors"`
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	TestSkip    int `json:"testSkip"`
	TestError   int `json:"testError"`
	Unsupported int `json:"unsupported"`
	Quarantined int `json:"quarantinedScripts"`
	ScopeBlocks int `json:"scopeBlocks"`
	Unresolved  int `json:"unresolvedBlocks"`
}

// QuarantinedScript names an untrusted script that stops a headless run.
type QuarantinedScript struct {
	Owner  string `json:"owner"`
	Name   string `json:"name,omitempty"`
	Listen string `json:"listen"`
	Hash   string `json:"hash"`
}

// DataInfo describes the dataset of a run (never the rows).
type DataInfo struct {
	Format  string   `json:"format"`
	Rows    int      `json:"rows"`
	Columns []string `json:"columns,omitempty"`
	Hash    string   `json:"hash"`
}

// PersistInfo records what happened to script variable writes.
type PersistInfo struct {
	Mode      string          `json:"mode"`               // keep | discard | ask
	Decision  string          `json:"decision,omitempty"` // ask mode: keep | discard
	Pending   []VarChangeView `json:"pending,omitempty"`  // writes made during the run
	Committed int             `json:"committed"`
	Skipped   []string        `json:"skipped,omitempty"` // scope.key that could not be stored
}

// Report is the complete, masked result of one run. Every report format
// (CLI, JSON, JUnit, HTML) is rendered from this one model.
type Report struct {
	RunUID         string              `json:"runUid"`
	CollectionUID  string              `json:"collectionUid"`
	CollectionName string              `json:"collectionName"`
	EnvUID         string              `json:"envUid,omitempty"`
	EnvName        string              `json:"envName,omitempty"`
	Source         string              `json:"source"`
	Status         string              `json:"status"`
	StopReason     string              `json:"stopReason,omitempty"`
	StartedMs      int64               `json:"startedMs"`
	FinishedMs     int64               `json:"finishedMs"`
	Iterations     int                 `json:"iterations"`
	Data           *DataInfo           `json:"data,omitempty"`
	Bail           string              `json:"bail,omitempty"`
	ScopePolicy    string              `json:"scopePolicy,omitempty"`
	Items          []ItemResult        `json:"items"`
	Totals         Totals              `json:"totals"`
	Persist        PersistInfo         `json:"persist"`
	Quarantined    []QuarantinedScript `json:"quarantined,omitempty"`
	Truncated      bool                `json:"truncated,omitempty"`
}

// Exit codes of `interseptor run`.
const (
	ExitPass        = 0
	ExitTestFail    = 1
	ExitRuntime     = 2 // runtime, script, transport, unresolved-variable or loop-guard errors
	ExitNotApproved = 3 // scripts not approved
	ExitScope       = 4 // scope-blocked
	ExitImportLint  = 5 // import / lint errors
)

// ExitCode maps a report to a process exit code. Precedence is by how little
// the result can be trusted: unapproved scripts (3), scope blocks (4), runtime
// problems (2), then assertion failures (1). "unsupported" script APIs count as
// runtime problems: never a pass, never a false assertion failure.
func (r *Report) ExitCode() int {
	if r.Status == StatusNotApproved {
		return ExitNotApproved
	}
	if r.Totals.ScopeBlocks > 0 {
		return ExitScope
	}
	if r.Totals.Errors > 0 || r.Totals.Unresolved > 0 || r.Totals.TestError > 0 || r.Totals.Unsupported > 0 ||
		r.Status == StatusLoopGuard || r.Status == StatusBadTarget || r.Status == StatusAborted {
		return ExitRuntime
	}
	if r.Totals.Fail > 0 {
		return ExitTestFail
	}
	return ExitPass
}

// tally folds one item into the totals.
func (t *Totals) add(it ItemResult) {
	t.Requests++
	switch it.Outcome {
	case collexec.OutcomeSent:
		t.Sent++
	case collexec.OutcomeSkipped:
		t.Skipped++
	case collexec.OutcomeBlocked:
		t.Blocked++
		switch it.BlockReason {
		case collexec.BlockUnresolved:
			t.Unresolved++
		case collexec.BlockQuarantined:
			t.Quarantined++
		default:
			t.ScopeBlocks++
		}
	case collexec.OutcomeError:
		t.Errors++
	}
	if it.BlockReason != collexec.BlockQuarantined {
		for _, s := range it.Scripts {
			if strings.HasPrefix(s.Reason, "quarantined") {
				t.Quarantined++
			}
		}
	}
	for _, x := range it.Tests {
		switch x.Status {
		case collexec.TestPass:
			t.Pass++
		case collexec.TestFail:
			t.Fail++
		case collexec.TestSkip:
			t.TestSkip++
		case collexec.TestError:
			t.TestError++
		case collexec.TestUnsupported:
			t.Unsupported++
		}
	}
}
