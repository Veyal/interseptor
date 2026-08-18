---
name: cancellable-background-startup
description: Make Interseptor background loops cancellation-aware during startup delays and join them before closing dependencies.
---

# Cancellable background startup

Use this when adding a delayed or periodic goroutine that accesses stores, listeners, or other owned
runtime dependencies.

- Replace an unconditional startup `time.Sleep` with a timer selected against the stop channel.
- Return or retain a `done` channel and wait for it before closing the dependency the goroutine uses.
- Start the worker only after prerequisite initialization succeeds, or arrange deferred cancellation
  on every later error path.
- Stop timers/tickers and close `done` from the worker itself so completion creates a real
  happens-before edge for tests and shutdown.
- For on-demand workers, publish `cancel` and `done` under the same state lock before launching the
  goroutine, reject new starts after owner shutdown, then cancel and join the published worker in
  `Close` before its project store is allowed to close.
- After claiming an on-demand worker, every target-loading or dependency error must call the same
  release path before responding. Distinguish a genuinely missing row from a database failure; an
  ignored query error often becomes a misleading empty-target response while leaving consent or
  running state changed.
- Replace delayed one-off `go sleep; callback` work with one owner-held timer. Stop the timer on close,
  guard callback admission with a closed flag, and join any callback that already passed that gate.
- For short on-demand maintenance jobs that cannot usefully be canceled, guard `WaitGroup.Add` and a
  closed flag with the same mutex. Shutdown sets the flag under that mutex before calling `Wait`, so
  no new job can race the wait; every admitted job calls `Done` and finishes before dependencies close.

Test cancellation during the initial delay with a short timeout; do not sleep for the production
delay or infer completion from some state the worker mutates before it actually returns.
