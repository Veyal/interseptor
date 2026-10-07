---
name: jsrt-script-runtime
description: Rules for running untrusted collection scripts through internal/jsrt (goja) without ambient I/O, with deterministic time/rand and hard limits.
---

# jsrt script runtime

- Callers use only `jsrt.Runtime` / `jsrt.New(Options)`. Never import `goja` outside `internal/jsrt`.
- One fresh runtime per script phase; a Runtime is not goroutine-safe and `Eval` is one-shot-per-phase by convention.
- Nothing is ambient: expose host capabilities explicitly via `Set` (`jsrt.Func` for Go callbacks). Do not add `require`, `fetch`, `process`, `Buffer` stubs that fake success; absent means `typeof x === "undefined"` (tested).
- Inject `Options.Clock` and `Options.Rand` for reproducible runs. Timers are virtual (never sleep, capped by `MaxTimers`/`VirtualSpan`); `Date.now()` only moves when timers fire.
- Interrupt is wall-clock (`Timeout`, ctx cancel). goja has no step counter and no heap cap: keep shim allocators bounded and rely on the WP9 worker for memory.
- goja runs promise jobs when a Go->JS call returns; `runTimers` drives the rest. Test any new async path with an interrupt-inside-loop case.
- Adding a goja capability: add a failing test in `internal/jsrt` first. Secrets must never reach `console`; scrub before `Set`-ing values or in the console sink.
