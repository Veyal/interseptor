# PRD-0003 — Passive Rule Packs

*Owner: Product · Status: shipped · Last updated: 2026-08-19*
*Links: [strategy.md](strategy.md) · [roadmap.md](roadmap.md)*

## Summary

Rule packs distribute versioned, signed passive Starlark checks independently of the Interseptor
binary. Installation is explicit, integrity-verified, and offline-friendly; packs never send network
requests or execute outside the existing sandbox.

## Goals

- Build and install a `.tar.gz` containing `checks/<id>.star` files and a manifest.
- Verify every file hash and optional publisher signature before writing any check.
- Keep installation and removal operator-gated; MCP remains read-only for pack management.
- Preserve reproducibility by recording the installed pack name and version.
- Keep local checks and built-in checks working when no pack is installed.

## Pack contract

```text
manifest.json
signature.json                  optional Ed25519 publisher signature
checks/<id>.star                passive Starlark checks
```

Each manifest entry contains the check id, kind `passive`, and SHA-256. Build and install reject any
other kind or any `active-checks/` content. Existing legacy files are not deleted or migrated; they
remain inert because the application no longer loads active checks.

## User experience and API

The Scanner → Checks manager shows installed packs and allows a human to install or remove them.
REST exposes `/api/packs`, `/api/packs/catalog`, and the guarded install/remove routes. MCP exposes
`list_packs` and `pack_info` as read-only discovery tools.

## Safety and compatibility

Starlark checks are sandboxed, bounded, and read-only over captured flows. A malformed, unsigned,
untrusted, or hash-mismatched pack is rejected before it can reach the checks directory. The current
release preserves old active-scan database rows and files without interpreting or executing them.
