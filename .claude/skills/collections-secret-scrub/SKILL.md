---
name: collections-secret-scrub
description: Rules for the collections tables (ix_*) so secrets never leave the project through archive, vault, merge or bundle paths.
---

# Collections secret scrub

- Collection tables live in `internal/store/collections*.go`, created lazily by `ensureCollections()` (CREATE IF NOT EXISTS, no change to `store.go` schema). Every public op calls it first.
- Secret variables: initial value is blanked at write time; the value lives only in `ix_var_current`. Never put a secret in `ix_variables`, `flow_ctx` or run summaries.
- There is exactly one scrub: `scrubSecrets` (DB snapshot) sharing `scrubJSON`/`scrubItemSecrets` (struct level). Any export copy must go through `BackupToScrubbed` / `ScrubSnapshotFile` (VACUUM with secure_delete so freed pages cannot hold canaries) or `ExportCollectionsBundle`. Never ship a raw `BackupTo` copy of a project that has collections.
- Script trust (`ix_script_trust`) is reset on every export, even with `IncludeSecrets`, and is never merged or imported. Merged collections get empty caps and scope policy `off` downgraded to `block`.
- `{{template}}` references survive scrubbing; names ending in url/uri/type/name etc. are not treated as secrets.
- New secret-bearing column or table: extend `scrubSecrets` and add the canary to `secretCanaries` in `collections_scrub_test.go`; a mutation (removing the delete) must make a test fail.
- Merge: `MergeCollectionsFrom(peerDB)` must be called by the project merge path; it is a no-op for peers without the tables. Never merge current values, cookies, tokens, trust or runs.
