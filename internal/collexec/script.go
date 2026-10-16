package collexec

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Veyal/interseptor/internal/jsrt"
	"github.com/Veyal/interseptor/internal/varstore"
)

// ResponseModel is the read-only response handed to test scripts (pm.response).
type ResponseModel struct {
	Code          int
	Status        string
	Headers       []varstore.KV // ordered by name
	Body          []byte        // decompressed, capped at MaxResponseBody
	BodyTruncated bool
	TimeMs        int64
	Size          int64
	Cookies       []*http.Cookie
	FlowID        int64
	HTTPVersion   string
}

// MaxResponseBody caps the body exposed to scripts.
const MaxResponseBody = 10 << 20

// ScriptInfo is pm.info.
type ScriptInfo struct {
	EventName   string // prerequest | test
	RequestName string
	RequestID   string
	Iteration   int
	RunID       string
}

// ScriptContext is the shared mutable state handed to every script of one
// phase of a Step (collection, then folders, then request). Executors mutate
// it directly; nothing else crosses the boundary.
type ScriptContext struct {
	Info     ScriptInfo
	Request  *RequestModel  // mutable in the prerequest phase
	Response *ResponseModel // set in the test phase
	Vars     *Vars
	Cookies  *Jar
	// Test results are appended with AddTest.
	tests []TestResult
	// Flow control, set by pm.execution.setNextRequest / skipRequest.
	skip    bool
	hasNext bool
	next    string
}

// AddTest records a test result from a script.
func (c *ScriptContext) AddTest(t TestResult) { c.tests = append(c.tests, t) }

// SkipRequest asks the pipeline not to send this request (pre-request only).
func (c *ScriptContext) SkipRequest() { c.skip = true }

// SetNextRequest records a setNextRequest call. An empty name means null
// (stop the run).
func (c *ScriptContext) SetNextRequest(name string) { c.hasNext, c.next = true, name }

// ScriptCall is one script execution request.
type ScriptCall struct {
	Phase  string // prerequest | test
	Owner  string // collection | folder | request
	Name   string
	Source string
	Hash   string
	Ctx    *ScriptContext
	// Caps is the collection's capability set.
	Caps []string
}

// ScriptOutcome is what an Executor returns besides its ScriptContext edits.
type ScriptOutcome struct {
	Console          []jsrt.ConsoleEntry
	ConsoleTruncated bool
}

// UnsupportedError reports an API a script needs that the engine does not
// provide. It becomes an "unsupported" test status, never a pass or a fail.
type UnsupportedError struct{ API string }

func (e *UnsupportedError) Error() string { return "unsupported API: " + e.API }

// ErrNoExecutor is the reason recorded when scripts exist but no engine is
// wired.
var ErrNoExecutor = errors.New("no script engine wired")

// Executor runs scripts. The goja/pm.* implementation lives outside this
// package and is injected through Pipeline.Exec.
type Executor interface {
	Run(ctx context.Context, call ScriptCall) (ScriptOutcome, error)
}

// ExecutorFunc adapts a function to Executor (tests, stubs).
type ExecutorFunc func(ctx context.Context, call ScriptCall) (ScriptOutcome, error)

// Run implements Executor.
func (f ExecutorFunc) Run(ctx context.Context, call ScriptCall) (ScriptOutcome, error) {
	return f(ctx, call)
}

// UnsupportedExecutor refuses every script as unsupported. It is the honest
// stub until a real engine is wired: scripts never "pass" without running.
type UnsupportedExecutor struct{}

// Run implements Executor.
func (UnsupportedExecutor) Run(context.Context, ScriptCall) (ScriptOutcome, error) {
	return ScriptOutcome{}, &UnsupportedError{API: "script engine not wired"}
}

// TrustChecker answers whether a script hash is trusted for a collection.
type TrustChecker interface {
	IsScriptTrusted(collectionUID, scriptHash string) (bool, error)
}

func scriptErr(err error) string { return fmt.Sprintf("%v", err) }
