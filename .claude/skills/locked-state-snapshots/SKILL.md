---
name: locked-state-snapshots
description: Return stable copies of Interseptor in-memory state instead of pointers that are mutated after a lock is released.
---

# Locked state snapshots

When a mutex-protected registry returns data to an HTTP handler, copy the struct while holding the
lock and return the copy. Do not return the registry's live pointer: another request can mutate it
while `encoding/json` reads it, creating both a data race and a response assembled from mixed states.

Copy mutable slice and map fields as well as the outer struct. Channels and other immutable identity
fields can remain shared when callers only wait on or serialize around them.

A deterministic regression can retain the returned snapshot, mutate the registry through its normal
method, and assert the earlier snapshot did not change; the race detector then covers simultaneous
serialization and mutation.
