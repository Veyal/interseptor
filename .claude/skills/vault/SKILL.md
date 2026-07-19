---
name: vault
description: Project vault — always-on archive store for multi-device backup/import/merge via Tailscale Serve.
---

# Project vault

## Pieces

| Layer | Path |
|-------|------|
| Store + HTTP | `internal/vault/` |
| CLI | `cmd/interseptor/vault.go` → `interseptor vault` |
| Control proxy | `internal/control/vault_client.go` |
| UI | Settings → API → Share → Project Vault (`apipanel.js`) |
| Docs | `docs/vault.md` |

## Conventions

- Archive format = full-project zip (`interceptor.db` + `bodies/**`), same as `/api/export/full`.
- Vault listens loopback (`127.0.0.1:9977`); expose with **Tailscale Serve**, not Funnel.
- Tokens `iv_…`, hashed in `tokens.json`; bootstrap raw in `vault.token` (0600).
- Client config machine-wide: `~/.interseptor/vault-client.json` (never echo key in GET).
- Proxies: `/api/vault/config|remote|backup|import|merge` — merge supports `dryRun`.
- MCP: `vault_list`, `vault_backup`, `vault_import`, `vault_merge`.
- Data dir: `--dir` / `INTERSEPTOR_VAULT_DIR` (default `~/.interseptor/vault`); `--keep` revisions.
