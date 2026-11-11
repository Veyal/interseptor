---
name: state-then-broadcast-tests
description: Testing code that updates state under a lock and broadcasts an SSE event after releasing it. Use when a test polls a GET endpoint for a phase or status and then reads the hub's event channel, when a control test fails only under load or `-race`, or when `events[len(events)-1]` panics with index -1.
---

# State is visible before its event

The project rule is "guard shared state with a mutex, invoke notifiers outside the lock". So a
writer does this:

```go
j.mu.Lock(); j.phase = updatePhaseError; ev := j.eventLocked(); j.mu.Unlock()
h.broadcast(ev)
```

Between the unlock and the broadcast, a reader of `GET /api/update` already sees the new phase and
the event is not queued yet. That window is a few hundred nanoseconds on an idle machine and
milliseconds under `go test ./...` with other packages running.

## The flaky shape

```go
waitPhase(t, ts, "error")      // polls the GET endpoint
events := drainEvents(ch)      // non-blocking read of what is queued so far
last := events[len(events)-1]  // panics: the event had not arrived
```

It passes thousands of times alone and fails in a full run. `TestUpdateFailureReportsErrorAndAllowsRetry`
failed exactly this way during a loaded commit gate.

## Rule

A test that asserts on an event waits for that event, with a deadline. Poll state only to assert
on state.

```go
events := drainEvents(t, ch, "error") // blocks until an event with that phase, or fails after 5 s
```

Do not "fix" it by broadcasting under the lock: a slow subscriber would then hold the lock, and
the notifier rule exists to prevent that. Do not add a sleep.

## When reviewing

- A non-blocking `select { case <-ch: … default: }` loop right after a poll is the smell.
- The same ordering applies in the UI: an SSE event can arrive before or after the response of the
  request that caused it. Handlers must be idempotent and keyed by a revision or sequence.
- Under load `go test -race ./internal/control/` needs `-timeout 40m`; the default 10 minutes was
  hit with no failure in the output.
