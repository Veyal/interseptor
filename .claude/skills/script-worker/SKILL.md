---
name: script-worker
description: Rules for internal/scriptworker, the out-of-process runner for untrusted collection scripts (re-exec worker, framed protocol, RSS/time kill, Executor swap).
---

# Script worker

- Callers depend on `scriptworker.Executor`, never on `pmsandbox.Run` directly. Untrusted (quarantined/imported/AI-sourced) scripts go to `Subprocess`; `Router.InProcessTrusted` is the only way trusted scripts run in-process.
- Worker = `interseptor __scriptworker`, handled in an `init()` in `cmd/interseptor/scriptworker.go` so it never reaches flag parsing or listeners. Tests re-exec the test binary (`TestMain` with `SW_TEST_MODE`, or `os.Args[1]=="__scriptworker"` in cmd).
- Wire: 4-byte length, 1 type byte, JSON. Cap 32 MiB; `ReadFrame` grows the buffer with received bytes so a lying length cannot force an allocation. Any protocol violation ends the session; add new frame types to `validType` and the fuzz seeds.
- The worker environment is NOT inherited (only `Subprocess.Env` plus `GOMEMLIMIT`). Never pass secrets in env.
- The worker never dials. Sends/scope checks are proxied; the parent re-runs `ScopeCheck` and enforces the send budget itself (do not trust the worker's own check).
- Keep the stdin pipe open for the whole run: EOF on stdin means the parent died and cancels the run (so in-process `Serve` tests need an `io.Pipe`).
- Non-serialisable `Input` parts: `Clock`/`Rand` are pinned (now / seed drawn from Rand), `Scrub` is applied by the parent to returned text. `Output.Redact` is unavailable on worker output (its redactor is unexported); scrub derived text with the caller's scrubber.
- Failure kinds in `Output.Errors[0].Kind`: `memory`, `timeout`, `canceled`, `error`. Status is always `error`; `Vars` are the unchanged input so callers do not lose state.
- Policy lives in `cmd/interseptor/scriptworker.go` (`newScriptRouter`): every approved script goes through the worker; `INTERSEPTOR_SCRIPTS_INPROCESS=1` lets owner-trusted scripts run in-process. Quarantined scripts never reach any executor. `PMExecutor.Scripts`/`StoreConfig.Scripts` carry the router; the zero router is in-process (tests). Verify with the built binary: while a script runs, `ps` shows `interseptor __scriptworker` as a child.
- Clock is pinned in the worker, so a `Date.now()` busy-wait never ends (it times out and the run continues); use an iteration loop to burn time.
