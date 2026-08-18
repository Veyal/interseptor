---
name: atomic-related-row-batches
description: Keep multi-row Interseptor metadata mutations all-or-nothing when one API action owns the batch.
---

# Atomic related-row batches

Use this when one store method applies multiple related rows, such as adding several tags to one
flow.

Run every insert/delete and any result query inside one SQLite transaction. Independent per-item
`Exec` calls can leave an early prefix committed when a later row fails, even though the method
returns an error. If the method returns the resulting set, read and scan it before commit too; an
error after commit is still a misleading partial-success contract.

Test with a trigger that rejects a late item in deterministic sort order, then assert the target
contains none of the earlier items after the error.

Multi-selection handlers need a store method whose transaction spans every selected object, not one
transaction per object. Broadcast refreshed objects only after that outer transaction commits; a
later-object failure must neither retain nor announce mutations to earlier selections.
