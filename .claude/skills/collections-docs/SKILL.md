---
name: collections-docs
description: Conventions for docs/collections.md, the generated pm.* parity table, docs registration and the cross-surface canary audit.
---

# Collections docs and canary audit

- The user guide is `docs/collections.md`. Root `collections.md` is generated: never edit it. After any docs change run `go run ./tools/docscheck generate` then `check`.
- A new public page needs four registrations: the `publicDocs` map in `tools/docscheck/main.go`, `_data/navigation.yml`, a README table row, and (for a feature) the same bullet order in `docs/FEATURES.md` and `_data/features.yml` (numbers sequential, titles equal).
- The pm.* parity table sits between the `pm-parity:begin/end` markers and is generated from `internal/pmsandbox/pm_coverage.json`. Never hand-edit it: `UPDATE_DOCS=1 go test ./internal/pmsandbox -run TestDocsParityTable` rewrites it and the same test fails when it is stale. Adding a pm.* API therefore touches the prelude, `pm_coverage.json`, the corpus and the docs table (via that command).
- Jekyll runs Liquid over every page, so a literal double-brace variable must be wrapped in a raw tag pair in prose; keep them to a minimum and never put one inside generated tables.
- Docs must describe what is reachable. There is no REST route or UI button for collection export yet (the `collexport` packages are only reachable through tests); say so rather than documenting one.
- Canary audit: archive, vault, bundle, merge, MCP, importers, auth and reports each have canary tests (`go test -run 'Canary|Scrub|Leak|Secret' ./internal/... ./cmd/...`). `TestCanaryAuditAIChannelReads` covers the AI/MCP read side and the project bundle in one place. Do not exercise sends in an audit test: a sent request is captured History evidence and keeps its wire bytes (a header secret appears in the bundle's HAR section by design), and it would dial the network.
- Generic example.com data only in docs and tests.
