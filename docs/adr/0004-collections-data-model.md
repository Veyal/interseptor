# ADR 0004: Collections data model

Status: accepted (plan section 3). Date: 2026-10-07.

- Project-DB tables created by idempotent `ensureX()` (the `ensureIntruderRuns` pattern), not settings blobs or `ui.repeaterTabs`: `ix_collections`, `ix_items`, `ix_environments`, `ix_variables`, `ix_var_current`, `ix_cookies`, `ix_tokens`, `ix_datasets`, `ix_assets`, `ix_runs`, `ix_run_results`, `ix_flow_ctx`, `ix_script_trust`, `ix_import_log`, `ix_item_revisions`. ULID uids, fractional `rank`.
- Initial and current variable values live in separate tables so one scrub rule excludes all current values.
- `store.FlagCollection = 1<<9`. Collection flows appear in History by default (COLL badge, filter chip); per-run `capture: all|failures|headers`.
- ONE `scrubSecrets(snapshot)` for archive, vault push, portable export and bundles: drop `ix_var_current`, `ix_tokens`, `ix_cookies`; blank secret initial values; reset `ix_script_trust`. Canary-string tests.
- Merge never carries current values, cookies, tokens, trust or runs. Plaintext at rest in the project DB, documented.
- Out of v1: gRPC, MQTT, mock server, monitors, Postman v3, `pm.visualizer`, Postman cloud.
