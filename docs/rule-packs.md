# Rule packs

A **rule pack** is a shareable bundle of Starlark checks (passive and/or active)
plus a manifest. It's how a community shares detection logic: build a pack from
a folder of `.star` files, hand the `.tar.gz` to a teammate (or publish it), and
they install it with one command — no fork, no copy-paste.

> Packs install checks that **run on every scan**, so install/remove are
> human-gated (CLI or the full-scope REST surface). The AI agent surface is
> **read-only** (`list_packs`, `pack_info`) — an agent can see what's installed
> and suggest checks, but can't install them unsupervised.

## Pack format

A pack is a `.tar.gz` containing:

```
manifest.json              name, version, author, entries[] (each: kind, id, sha256)
checks/<id>.star           passive checks
active-checks/<id>.star    active checks
```

Every check file's **sha256** is recorded in the manifest at build time and
**verified on install**, so a corrupted or tampered pack is rejected before any
check reaches disk. (Detached minisign/ed25519 signing is the next layer; the
manifest+sha256 gate is the foundation it rides on — see
[PRD-0003](product/prd-0003-rule-packs.md).)

## CLI

```bash
# Build a pack from a folder that has checks/ and/or active-checks/ subdirs.
interseptor rules create --name owasp-top --version 1.0.0 --author Priya ./mychecks --out owasp.tar.gz

# Install (verifies integrity, writes checks into ~/.interseptor/checks & active-checks).
interseptor rules install owasp.tar.gz

# See what's installed.
interseptor rules list

# Inspect one pack.
interseptor rules info owasp-top

# Uninstall (deletes exactly the check files that pack owned).
interseptor rules remove owasp-top
```

Installed checks live in the same global `~/.interseptor/checks` and
`~/.interseptor/active-checks` directories the engines already read, so they run
immediately alongside your other checks and custom-built-ins — no restart.

## REST + MCP

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/packs` | list installed packs |
| `GET` | `/api/packs/{name}` | one pack's record |
| `POST` | `/api/packs/install` | upload a `.tar.gz` (full-scope key) |
| `DELETE` | `/api/packs/{name}` | uninstall (full-scope key) |

MCP tools: `list_packs`, `pack_info` (read-only — see the note above).

## Authoring checks for a pack

A check in a pack is just a normal Starlark check (see
[custom checks](custom-checks.md) / [active checks](custom-active-checks.md)),
optionally with a `# key: value` front-matter header for provenance:

```python
# name: JWT in response
# description: Flags a JWT returned in a response body or header.
# author: Priya
# version: 1.0.0
# severity: medium
def check(flow):
    if re_search("ey[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+", flow.res_body):
        return [finding("medium", "JWT in response body", evidence="see response")]
    return []
```

The front-matter is parsed and surfaced in `list_checks` / `list_active_checks`
so a pack listing can show what each check does without reading its source.
