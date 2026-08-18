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

When related-row tables intentionally lack foreign keys, verify each target primary row (flow or
finding) through the same transaction before inserting metadata. Otherwise stale API IDs can create
orphan rows that surface in aggregate counts even though the target object does not exist.

Do the same for body-derived association rows. Before syncing `finding_flows`, verify the parent
finding inside the transaction; checking only the referenced flow still allows a stale finding ID to
create orphan evidence. For simple updates, inspect `RowsAffected` and return `sql.ErrNoRows` when
the target disappeared.
