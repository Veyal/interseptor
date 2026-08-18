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

Test cancellation during the initial delay with a short timeout; do not sleep for the production
delay or infer completion from some state the worker mutates before it actually returns.
