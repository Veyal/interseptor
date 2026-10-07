---
name: collections-control-api
description: Rules for the collections REST routes, MCP tools and script trust in internal/control (collections*.go, envs.go, trust.go) and internal/mcp/collections.go.
---

# Collections control API

- Routes live in one table, `collRoutes` (`collections_routes.go`). It feeds mux registration and `apiRoutes` (via `init`), so add a route there only. `TestCollectionRoutesCatalogMatchesMux` and `TestCollectionRoutesRefuseForeignHost` iterate it. New MCP tools go in `internal/mcp/collections.go` and in `collectionMCPTools` (`TestMCPDescriptorMatchesRegistry`).
- Trust is UI-session only. `requireUISession` rejects any `X-Interseptor-Source` other than ui, any bearer API key, and a missing `X-Interseptor-CSRF`. Never add an MCP tool or AI-reachable route that trusts scripts or sets capabilities. Collection `caps` have no input field on create/update; only `POST .../trust` sets them (grantable names only, `net.outOfScope` is not grantable).
- Hashes bind the capability set, so changing caps re-binds every script. `approve` matches the caller's selection by pre-change hash and trusts the re-derived hash.
- Auto-trust (`autoTrustOwnEdit`) only trusts hashes that are new in an edit from a UI session. Renames or AI edits never approve an existing script.
- AI-source callers (`isAISource`) get scrubbed reads (`ExportCollectionsBundle(ScrubOptions{})`), the collection's scope policy (default block, no override), no `reveal`, no script source. Humans get their own data back.
- Secret values: `setCurrentValue` never echoes secrets; `viewVars` masks them unless `reveal=1` in a UI session; step results are already masked by `collexec`, and AI responses get a second `Registry.Mask` pass.
- `collectionsAPI.mu` is not reentrant: never call a locking helper (`setCurrentValue`, `approve`) while holding it (this deadlocked once in `runOne`).
- All JSON handlers use `decodeLimitedJSON` with the package limits `maxCollection*Bytes` (vars so tests can shrink them); import uses `readLimitedBody`.
- Tests that send to an `httptest` loopback server need an explicit include scope rule for the host (the pipeline's dial guard blocks private hosts under `block`), and `SetSelfAddr` to model own-listener refusal.
- Script variable writes: UI send defaults to `persist: keep`, runs and MCP default to `discard`; a run carries writes between steps through `varOverlay` even when discarding.
