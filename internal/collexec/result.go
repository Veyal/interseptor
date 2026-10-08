package collexec

import (
	"github.com/Veyal/interseptor/internal/varstore"
)

// Outcome is the terminal state of one Step.
type Outcome string

const (
	// OutcomeSent means the request went out and a response (or a recorded
	// transport error flow) exists.
	OutcomeSent Outcome = "sent"
	// OutcomeSkipped means a pre-request script asked to skip the request.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeBlocked means policy refused the send (unresolved variables,
	// scope, base-target pin, own listener, quarantined scripts when required).
	OutcomeBlocked Outcome = "blocked"
	// OutcomeError means the step failed before sending (bad URL, script
	// error in a pre-request script, codec failure).
	OutcomeError Outcome = "error"
)

// BlockReason names why an Outcome is blocked.
type BlockReason string

const (
	BlockUnresolved  BlockReason = "unresolved_variables"
	BlockScope       BlockReason = "out_of_scope"
	BlockPin         BlockReason = "base_target_pin"
	BlockOwn         BlockReason = "own_listener"
	BlockQuarantined BlockReason = "scripts_quarantined"
)

// TestStatus is the status of one test or assertion. "unsupported" is distinct
// from "fail": a missing script API must never read as a real failure or pass.
type TestStatus string

const (
	TestPass        TestStatus = "pass"
	TestFail        TestStatus = "fail"
	TestSkip        TestStatus = "skip"
	TestError       TestStatus = "error"
	TestUnsupported TestStatus = "unsupported"
)

// TestResult is one test or declarative assertion outcome.
type TestResult struct {
	Name     string     `json:"name"`
	Status   TestStatus `json:"status"`
	Severity string     `json:"severity,omitempty"`
	Subject  string     `json:"subject,omitempty"`
	Expected string     `json:"expected,omitempty"`
	Actual   string     `json:"actual,omitempty"`
	Message  string     `json:"message,omitempty"`
	// Source is "line:col" for script tests or "assertion:<id>".
	Source     string `json:"source,omitempty"`
	Owner      string `json:"owner,omitempty"` // collection | folder | request
	DurationMs int64  `json:"durationMs,omitempty"`
}

// VarChange is one variable write made by a script. Value is excluded from
// JSON; Display is the masked form safe to show.
type VarChange struct {
	Scope   string `json:"scope"` // environment | globals | collection | local
	Key     string `json:"key"`
	Value   string `json:"-"`
	Display string `json:"value,omitempty"`
	Unset   bool   `json:"unset,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
}

// ScriptNote records a script that did not run, or how one ended.
type ScriptNote struct {
	Owner  string `json:"owner"`
	Name   string `json:"name,omitempty"`
	Listen string `json:"listen"`
	Hash   string `json:"hash"`
	Reason string `json:"reason"`
}

// ResponseSummary is the masked, bounded view of the final response. Bodies
// are read from the stored flow by FlowID, never carried here.
type ResponseSummary struct {
	Status      int    `json:"status"`
	StatusText  string `json:"statusText,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size"`
	TimeMs      int64  `json:"timeMs"`
	Error       string `json:"error,omitempty"` // transport error recorded on the flow
}

// FlowControl carries pm.execution.setNextRequest / skipRequest decisions.
type FlowControl struct {
	// HasNext is true when a script called setNextRequest. NextRequest is the
	// target name/uid; empty with HasNext means "stop the run" (null).
	HasNext     bool   `json:"hasNext,omitempty"`
	NextRequest string `json:"nextRequest,omitempty"`
}

// StepResult is the full, secret-free record of one Step.
type StepResult struct {
	Outcome     Outcome          `json:"outcome"`
	BlockReason BlockReason      `json:"blockReason,omitempty"`
	Error       string           `json:"error,omitempty"`
	Method      string           `json:"method,omitempty"`
	URL         string           `json:"url,omitempty"` // resolved, masked
	FlowID      int64            `json:"flowId,omitempty"`
	FlowIDs     []int64          `json:"flowIds,omitempty"` // every hop incl. redirects
	Response    *ResponseSummary `json:"response,omitempty"`
	Tests       []TestResult     `json:"tests,omitempty"`
	Console     []ConsoleLine    `json:"console,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
	Uses        []varstore.Use   `json:"uses,omitempty"`
	Unresolved  []string         `json:"unresolved,omitempty"`
	VarChanges  []VarChange      `json:"varChanges,omitempty"`
	Scripts     []ScriptNote     `json:"scripts,omitempty"` // skipped/quarantined/failed
	Flow        FlowControl      `json:"flow"`
	Applied     []string         `json:"applied,omitempty"` // e.g. codec:aes-form, auth:bearer
}

// Failed reports whether any test failed or errored.
func (r *StepResult) Failed() bool {
	for _, t := range r.Tests {
		if t.Status == TestFail || t.Status == TestError {
			return true
		}
	}
	return false
}

// ConsoleLine is one masked console entry, tagged with the script that wrote it.
type ConsoleLine struct {
	Owner string `json:"owner"`
	Level string `json:"level"`
	Text  string `json:"text"`
}
