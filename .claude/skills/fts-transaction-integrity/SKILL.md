---
name: fts-transaction-integrity
description: Keep Interseptor flow rows and their SQLite FTS projection transactionally consistent.
---

# FTS transaction integrity

Use this when a store mutation changes fields indexed by `flows_fts`.

Update the canonical `flows` row and its FTS row inside one SQLite transaction. A sequence of
independent `Exec` calls can commit the canonical change, fail while deleting or reinserting FTS,
and return an error with search permanently out of sync.

For replacement, read the unchanged indexed fields through the transaction, update the canonical
row, delete the old FTS row by `rowid`, insert the full replacement projection, and commit. Publish
no caller-visible success state before commit.

Test deterministically by dropping `flows_fts` after seeding a flow: the mutation must return an
error and the canonical flow fields must retain their previous values.
