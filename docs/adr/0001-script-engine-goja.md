# ADR 0001: Script engine is pure-Go goja behind `internal/jsrt`

Status: accepted (owner decision, to be reviewed after release). Date: 2026-10-07.

## Context
Collections need pre-request and post-response scripts that run existing Postman JS unchanged. Constraints: single static binary, `CGO_ENABLED=0`, scripts are untrusted, runs must be reproducible.

## Decision
Use `github.com/dop251/goja` behind the engine-neutral `internal/jsrt.Runtime` interface. A `pm.*` shim (WP5) is built on top. Fallbacks, in order, only if a must-have fails: `modernc.org/quickjs` (pure Go), then declarative blocks only. Never a regex or source translator.

## Spike result (measured, `go test ./internal/jsrt`, goja 44620e89763c)
All must-haves pass in `TestSpike*`/`TestInterrupt*`:

| Criterion | Result |
|---|---|
| async/await, Promise jobs, Promise.all | pass |
| optional chaining, `??`, classes with `#private`, generators, BigInt | pass |
| regexp lookbehind, named groups | pass |
| JSON/Date fidelity | pass |
| Interrupt: sync loop, promise-job loop, `await` loop, timer-callback loop, ctx cancel | pass (all stop within the deadline) |
| Injectable clock (Date, performance.now) and rand (Math.random) | pass, byte-identical across runs |
| Virtual timers (setTimeout/setInterval, ordered, never sleep, capped) | pass (implemented in `jsrt`; goja has no event loop) |
| No ambient I/O (`require`, `process`, `fetch`, `Buffer`... all undefined) | pass |
| Stack cap, source cap, console cap, per-runtime prototype isolation | pass |
| Startup per runtime | about 9 us/op, 21.8 KB, 234 allocs (benchmark `BenchmarkNewRuntimeEval`, M-series) |

QuickJS was not spiked: goja passed every must-have, so the fallback trigger did not fire.

## Known limits (accepted)
- No step counter hook in goja: "step interrupt" is wall-clock `Interrupt` (default 5 s, ceiling 60 s) plus ctx cancel. Adequate because every loop kind is interrupted (tested).
- No heap cap. Mitigation: bounded allocators in the shim (WP5), then the re-exec'd worker with RSS watchdog (WP9). Until WP9, scripts stay quarantined/trusted-only (ADR 0003).
- `Date`/`performance` use a virtual offset advanced by timers; `Date.now()` does not advance during synchronous busy work.
- ES2023-level only; no Intl beyond goja's, no `fetch`, no modules.

## Consequences
Engine can be swapped by implementing `jsrt.Runtime`. `jsrt.New(Options)` is the only constructor callers use.
