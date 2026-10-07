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
