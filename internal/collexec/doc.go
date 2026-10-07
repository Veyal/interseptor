// Package collexec is the collection execution pipeline. One Step() call turns
// a collection item (plus its folder/collection ancestors) into a sent,
// captured, tested request. The UI send, runner, CLI, MCP and pm.sendRequest
// all use this one path so behaviour never drifts between callers.
//
// Pipeline (see the Postman-replacement plan, section 5):
//
//	plan chain -> trust gate -> pre-request scripts (outer to inner) ->
//	resolve {{vars}} -> auth -> cookie jar -> scope guard -> codec encode ->
//	sender.Send (FlagCollection, flow context) -> Set-Cookie -> test scripts
//	(outer to inner) -> declarative assertions -> var changes + flow control.
//
// Scripts run through the Executor interface. The real goja/pm.* executor is
// wired by the control layer; collexec never imports an engine, so it builds
// and tests without one. When no Executor is wired, scripts are reported as
// skipped, never silently passed.
//
// Invariants:
//   - Capture is best effort. A failing flow-context write, a panicking hook
//     or an unwritable store never turns a successful send into an error.
//   - Secrets never appear in a StepResult: every text field is masked through
//     the redact registry, and variable values are excluded from JSON.
//   - Scripts that are not trusted never run (the request still does).
package collexec
