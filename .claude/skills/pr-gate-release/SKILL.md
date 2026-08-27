# PR gate → merge/reject → release

Continuous maintainer loop for `Veyal/interseptor`. Prefer action over asking.

## Cadence

Every ~5 minutes (or on wake): triage open PRs, then release if `main` moved past the latest tag with shippable changes.

## Per open PR

1. **Collect:** `gh pr view N`, `gh pr diff N`, CI rollup, issue/PR comments (skip resolved noise).
2. **Validate (must all pass to merge):**
   - Intent is clear and in-scope for Interseptor (proxy/UI/MCP/docs/packaging).
   - Diff is minimal and matches the claim (no drive-by refactors, no secrets, no real target data).
   - Follows AGENTS.md: no cgo, no import cycles, capture off hot path, Conventional Commits.
   - UI/JS: syntax-valid; smoke the changed module mentally (parens/IIFEs, imports).
   - Go: would pass `go test` / `go vet` for touched packages; CI green is necessary but not sufficient.
   - Tests exist for non-trivial Go behavior; pure one-line syntax fixes OK without new tests.
   - CHANGELOG under `[Unreleased]` if the PR ships user-visible behavior (add it yourself before merge if missing and the change warrants it).
3. **Reject** (`gh pr close N --comment …`) when:
   - Wrong/broken fix, speculative, duplicates merged work, breaks forwarding/hot path, or CI red with no clear fix in-PR.
   - Author intent unclear and cannot be inferred from the diff alone.
   - Comment must state the concrete reject reason and what would make a follow-up acceptable.
4. **Merge** only when mergeable + CI green + validation passed: `gh pr merge N --merge --delete-branch`.
5. Sync `~/interseptor` and `~/Documents/interseptor` with `git pull --ff-only origin main`.

## Release gate (after merges or when `main` ≠ latest tag)

Ship a patch/minor when commits since the latest `v*` tag include user-visible fixes/features (UI breakage, MCP, proxy, docs that operators rely on). Skip pure chore-only gaps unless asked.

Release version, tag, and changelog sequencing is owned by
[CONTRIBUTING “Cutting a release”](../../../CONTRIBUTING.md#cutting-a-release); follow it exactly.
After those documented steps:

1. `TAG=vX.Y.Z HOMEBREW_TAP_TOKEN="$(gh auth token)" SCOOP_BUCKET_TOKEN="$(gh auth token)" ./packaging/scripts/publish-packages.sh` when packaging should track the release.
2. `update-filemap` after notable path/sync changes.

## Tick report (short)

- Open PRs reviewed (merge / reject / wait+why)
- Release action or “no release”
- Next wake still armed
