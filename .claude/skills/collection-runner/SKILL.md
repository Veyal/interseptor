---
name: collection-runner
description: Rules for internal/collrun, internal/collreport and `interseptor run`/`lint`: one runner for UI/CLI/MCP, exit codes, masked reports, loop guard.
---

# Collection runner, CLI and reports

- One runner: `collrun.Runner` (sync, CLI) and `collrun.Manager` (async, server) wrap the same `collexec.Step`. Never add a second run loop in `control` or `cmd`.
- Exit codes are a contract: 0 pass, 1 test failures, 2 runtime/unresolved/unsupported/loop-guard, 3 scripts not approved (before any send), 4 scope-blocked, 5 lint/usage. A new failure class needs a code decision and a CLI exit-code test.
- Loop guard: `setNextRequest` steps are capped at 10 x plan x iterations (hard 100000) plus a per-item visit cap; hitting it is exit 2, never a hang.
- Persist: CLI default discard, runner UI default ask. Discard is exact because script writes go to a copy-on-write overlay; token chains still work inside the run.
- Reports: build one `collreport.Model`, scrub every string once (registry secrets + credential shapes), then render CLI/JSON/JUnit/HTML. Add a canary string test when adding a field. JUnit is validated against `testdata/junit-10.xsd`.
- Headless CLI never opens listeners, refuses to send without a declared scope, and a `--trust-hash` pin lives only for that process.
- UI: `runner-model.js` is pure (node-tested); `runner.js` mounts into a host element and uses text labels for every state, never colour alone. "unsupported" is never rendered as a pass.
- Control uses the same backend and runner as the CLI: `collectionsAPI.backend()` is a `collrun.StoreBackend` (shared jars, registry, own-listener func, `Hub.ScriptRouter`). Never add a run loop or a second script executor in `control`; sync `/api/collections/run` and async `/api/runner/runs*` both go through `collrun.Manager`.
- Async API: start returns 202 with a run uid owned by the Manager (closing the view or the request never aborts it). Follow by `GET /api/runner/runs/{uid}?since=N`, the per-run SSE `/events` (snapshot first, then events), or the global `{type:"collrun", event}` broadcast. The persist-ask answer (`POST .../persist`) is UI-session only; the AI channel cannot ask or answer.
- Cookies follow the persist policy like variable writes (`collrun.SessionBackend`): keep stores the jar in `ix_cookies`, discard restores the pre-run jar, ask follows the answer (no prompt shown = keep). The interactive single send defaults to keep.
- Runner view: `runner.js` follows a run with `applyProgress`/`applyEvent` (pure, node-tested, idempotent by item `seq`); polling is the fallback and the SSE nudge is only an accelerator. Keep every state a text label.
- The CLI opens the project database directly, so it can run while a live instance has the same project open. Flow ids are allocated per process; `Store.insertFlow` retries on `flows.id` conflicts after resyncing to the table (`TestInsertFlowSurvivesIDTakenByAnotherWriter`). The proxy write-behind queue pre-allocates ids and does not retry, which is why live proxy capture plus a concurrent CLI run can still lose rows.
