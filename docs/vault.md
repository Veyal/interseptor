# Project vault

Always-on archive store for Interseptor projects. Run `interseptor vault` on a durable host
(Mac mini, VPS, NAS), expose it over **Tailscale Serve** (tailnet-only), and use any laptop’s
Interseptor UI / REST / MCP to **backup**, **list**, **import**, or **merge** without USB zips
or a live peer process.

This is complementary to peer sync (`/api/merge/*`): peers need both Interseptors online; the
vault keeps revisions on disk.

## Server

```bash
# default: ~/.interseptor/vault, listen 127.0.0.1:9977, keep last 10 revs
interseptor vault

# external disk later
interseptor vault --dir /Volumes/Backup/interseptor-vault --keep 20
# or: INTERSEPTOR_VAULT_DIR=/Volumes/Backup/interseptor-vault
```

On first start a bootstrap token (`iv_…`) is printed and written to
`<dir>/vault.token` (mode `0600`). Use that as `Authorization: Bearer …` from clients.

### Tailscale Serve (recommended)

Keep the vault on loopback; Serve publishes HTTPS on the tailnet only (not Funnel):

```bash
# on the vault host
interseptor vault --addr 127.0.0.1:9977

# proxy https://<this-host>:9977 → local vault
tailscale serve --bg --https=9977 http://127.0.0.1:9977
```

Clients on the tailnet use `https://mac-mini.tailXXXX.ts.net:9977` (or your MagicDNS name)
plus the vault token.

### Vault HTTP API

| Method | Path | Scope | Notes |
|--------|------|-------|--------|
| `GET` | `/api/vault/status` | read | dir, keep, counts |
| `GET` | `/api/vault/projects` | read | list projects + latest rev |
| `GET` | `/api/vault/projects/{id}` | read | revision list |
| `PUT` | `/api/vault/projects/{id}?label=` | full | body = full-project zip |
| `GET` | `/api/vault/projects/{id}/latest` | read | download zip |
| `GET` | `/api/vault/projects/{id}/revs/{n}` | read | download zip |
| `DELETE` | `/api/vault/projects/{id}/revs/{n}` | full | drop one rev |
| `DELETE` | `/api/vault/projects/{id}` | full | drop project |

Archive format matches **Export full project**: `interceptor.db` + `bodies/**`.

## Client (any Interseptor)

Machine-wide config: `~/.interseptor/vault-client.json` (`url` + `key`).

**UI:** Settings → API & MCP → **Share** → **Project Vault** — save URL/token, Backup, list,
Import as new, Merge into current (dry-run confirm).

**REST (proxied by the control plane):**

| Method | Path | Body |
|--------|------|------|
| `GET`/`PUT` | `/api/vault/config` | `{url?, key?}` — key never echoed |
| `GET` | `/api/vault/remote` | list remote projects |
| `POST` | `/api/vault/backup` | `{id?, label?}` |
| `POST` | `/api/vault/import` | `{id, name?, rev?, overwrite?}` |
| `POST` | `/api/vault/merge` | `{id, rev?, label?, dryRun?}` |

**MCP:** `vault_list`, `vault_backup`, `vault_import`, `vault_merge`.

## Layout on disk

```
~/.interseptor/vault/
  meta.json
  vault.token          # bootstrap raw token (local only)
  tokens.json          # hashed tokens
  projects/<id>/
    meta.json
    rev-000001.zip
    …
```
