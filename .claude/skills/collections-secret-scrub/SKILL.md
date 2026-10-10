---
name: collections-secret-scrub
description: Rules for the collections tables (ix_*) so secrets never leave the project through archive, vault, merge or bundle paths.
---

# Collections secret scrub

- Collection tables live in `internal/store/collections*.go`, created lazily by `ensureCollections()` (CREATE IF NOT EXISTS, no change to `store.go` schema). Every public op calls it first.
- Secret variables: initial value is blanked at write time; the value lives only in `ix_var_current`. Never put a secret in `ix_variables`, `flow_ctx` or run summaries.
- There is exactly one scrub: `scrubSecrets` (DB snapshot) sharing `scrubJSON`/`scrubItemSecrets` (struct level). Any export copy must go through `BackupToScrubbed` / `ScrubSnapshotFile` (VACUUM with secure_delete so freed pages cannot hold canaries) or `ExportCollectionsBundle`. Never ship a raw `BackupTo` copy of a project that has collections.
- Script trust (`ix_script_trust`) is reset on every export, even with `IncludeSecrets`, and is never merged or imported. Merged collections get empty caps and scope policy `off` downgraded to `block`.
- `{{template}}` references survive scrubbing, including `Bearer {{token}}`-style values (`onlyTemplateRefs`: refs plus auth-scheme words only; a literal next to a ref is still blanked). A regression here silently strips auth from every request the scrub touches; names ending in url/uri/type/name etc. are not treated as secrets.
- New secret-bearing column or table: extend `scrubSecrets` and add the canary to `secretCanaries` in `collections_scrub_test.go`; a mutation (removing the delete) must make a test fail.
- Merge: `MergeCollectionsFrom(peerDB)` must be called by the project merge path; it is a no-op for peers without the tables. Never merge current values, cookies, tokens, trust or runs.
- Every archive/vault/merge/bundle path snapshots through `Store.BackupToScrubbed` (`control.snapshotDB`), the project bundle carries only `ExportCollectionsBundle` (v2 `collections` section) and `MergeFrom` unions collections via the same scrubbed merge. Canary tests drive `/api/export/full`, `/api/export/full/file`, `/api/vault/backup` and `/api/export/project`.
- Variables declared on a script's or tool's behalf are secret-typed when `store.IsSecretName(key)` (the scrub heuristic), so a script-written `token` is masked in listings and blanked in exports. Captured flows keep their wire bytes (evidence) by design.
- Scrub on export, not on a user import. `ImportUserCollectionsBundle` (the file a person chose: Postman, Insomnia, Bruno, HAR, Burp, OpenAPI, curl) keeps literal credentials at rest; they are the user's own data and blanking them turned every authenticated request into a silent 401. `ImportCollectionsBundle`, `MergeCollectionsFrom` and the project-bundle restore stay untrusted and scrub on the way in (`mergeCollBundle(b, scrub)`). New import entry points must pick one on purpose; the safe default is the scrubbing one. Every export path still scrubs, so keeping credentials at rest widens nothing that leaves the project.
- The item merge signature (`itemSig`) scrubs its own inputs, so a request matches whether or not its secrets were blanked on the way in. `URLCredentialFields` / `BodyCredentialFields` name credential fields (never values) for import reports.
