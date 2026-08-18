---
name: disk-backed-registry-locking
description: Keep Interseptor's small in-memory/disk launcher registry mutations serialized through their atomic persistence boundary.
---

# Disk-backed registry locking

Use this when changing `internal/launcher.Registry` mutations or similar small disk-backed state.

Hold the registry mutex from the in-memory mutation through the temp-file write and atomic rename.
Unlocking before persistence creates two defects:

- concurrent mutations can race on the same `.tmp` filename and lose or fail a save; and
- `Get` can report the new state while the mutation goroutine is still writing, so callers cannot
  use the read as a completion barrier during shutdown or test cleanup.

Read-only operations should copy the state while holding the mutex and release it immediately.
Keep slow or untrusted callbacks outside the lock unless their result is required to decide the
single serialized mutation; launcher liveness callbacks are local, bounded process checks.

Run the launcher package repeatedly under `go test -race` because cleanup timing exposes this class
of defect more reliably than a single non-race run.

Apply the same rule to the external-project MRU file: serialize read-modify-write in-process, publish
through a same-directory temporary file, and return persistence errors to the switch handler before it
schedules re-exec. A registry update that silently fails is not best-effort—the selected project will
disappear from the switcher after restart.
