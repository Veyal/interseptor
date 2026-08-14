---
layout: default
title: Getting started
classification: current
source: docs/getting-started.md
---
<p class="eyebrow">CURRENT</p>
# Getting started

## Install

Interseptor is a single static binary — **no cgo, no Node, no runtime dependencies**.

### Recommended — `go install` (uses the release tags)

Requires **Go 1.25+**:

```bash
# latest release:
go install github.com/Veyal/interseptor/cmd/interseptor@latest
# …or pin a specific release:
go install github.com/Veyal/interseptor/cmd/interseptor@v0.1.0

interseptor        # if $(go env GOPATH)/bin is on your PATH
```

**Or update in place** (no `go install` to remember):

```bash
interseptor update              # latest release
interseptor update --check      # is a newer version out?
interseptor update --version 0.6.0
```

`interseptor update` downloads a prebuilt binary from [GitHub Releases](https://github.com/Veyal/interseptor/releases) when one is attached for your OS/arch (and verifies `checksums.txt` when present). If the release has no binary yet, it falls back to `go install` automatically.

Every tagged version is listed on the [**Releases**](https://github.com/Veyal/interseptor/releases)
page with its changelog; `@latest` resolves to the newest tag, `@vX.Y.Z` pins one.

### From source

```bash
git clone https://github.com/Veyal/interseptor.git
cd interseptor
CGO_ENABLED=0 go build -o interseptor ./cmd/interseptor
./interseptor
```

### Prebuilt binaries

Each tagged release attaches static binaries for **linux / macOS / windows** (amd64 & arm64) plus a
`checksums.txt` on the [Releases](https://github.com/Veyal/interseptor/releases) page (built by the
release workflow when a `v*` tag is pushed) —
download, verify the checksum, `chmod +x`, and run. (`go install` above is equivalent and always
tracks the latest release.)

## Quick start

1. **Run it.** `interseptor` starts the proxy on `127.0.0.1:8080` and the UI on `127.0.0.1:9966`.
   Open that URL in your browser — or start with `--open` to launch it automatically.
2. **Send traffic through it.** Point your browser/HTTP client (or the OS proxy via **Settings →
   System proxy** on macOS) at `127.0.0.1:8080`.
3. **For HTTPS, trust the CA** (see below) — then HTTPS flows are decrypted and editable.
4. **Work the loop.** Watch flows land in **Proxy**, send one to **Repeater** or **Intruder**, run
   the **Scanner**, set **Scope**, or flip on **Intercept** to hold/edit requests and responses.

For a LAN or mobile listener, read [Proxy authentication]({{ "/proxy-and-tls/" | relative_url }}#proxy-authentication)
before rebinding: non-loopback listeners require a full-scope API key as the proxy password. The
browser realm is `interseptor`; it is not asking for the target site's credentials.

Runtime data lives under `~/.interseptor/` (`interseptor.db`, `bodies/`, `ca/`). Full-project archives
intentionally use the compatibility filename `interceptor.db`. Delete the runtime directory to reset.

## Intercepting HTTPS

1. Point your client at the proxy (`127.0.0.1:8080`).
2. Download the CA from the **Settings** tab (or `http://127.0.0.1:9966/api/ca.crt`) and
   install/trust it in your OS/browser trust store.
3. HTTPS flows are now decrypted, captured, and editable. Per-host leaf certs are minted on demand
   and cached.

## Configuration

| Environment variable | Effect |
|---|---|
| `INTERSEPTOR_OPEN_BROWSER` | Auto-open the UI on start (same as `--open`). The default is **not** to open it. |
| `INTERSEPTOR_NO_BROWSER` | Hard-disable browser auto-open, overriding `--open`/`INTERSEPTOR_OPEN_BROWSER`. |
| `INTERSEPTOR_ALLOW_EXTERNAL_BIND` | Lock down to **loopback-only** binds when set to `0`/`false`. External bind (e.g. `0.0.0.0` for LAN capture) is allowed by default — see [Security model]({{ "/architecture/" | relative_url }}#security-model). |
| `INTERSEPTOR_CONTROL_URL` | For `interseptor mcp`: the control API to drive (default `http://127.0.0.1:9966`). External MCP clients connect through this server; model providers are configured outside Interseptor. |
| `INTERSEPTOR_CONTROL_ADDR` | Env equivalent of `--control-addr`: full control UI/API listen address (`host:port`). |
| `INTERSEPTOR_PROJECT` | Env equivalent of `--project`: open a specific project by name/path. |
| `INTERSEPTOR_PROXY_ADDR` | Override the proxy listen address(es) (also how the launcher gives each spawned instance its own port). |
| `INTERSEPTOR_NO_UPDATE_CHECK` | Disable the background update check Interseptor runs on every startup. |
| `GITHUB_TOKEN` / `INTERSEPTOR_GITHUB_TOKEN` / `GH_TOKEN` | Raises the GitHub API rate limit used for update checks (first non-empty wins). |

The proxy bind address is also runtime-configurable in **Settings** (and persisted).

For the complete command surface, see the [CLI reference]({{ "/cli-reference/" | relative_url }}). For CA trust, origin
verification, passthrough, pinning, and upstream chaining, see [Proxy, TLS, and networking]({{ "/proxy-and-tls/" | relative_url }}).

## Running multiple projects

For one-off multi-instance use, `interseptor` takes root flags: `--project <name|path>` (or
`INTERSEPTOR_PROJECT`) opens a specific project; `--control-port <port>`
picks the control UI/API port on loopback (default `9966`); `--control-addr host:port` sets the full
control listen address and overrides `--control-port` (or `INTERSEPTOR_CONTROL_ADDR` — see
[Configuration](#configuration)). Pair `--control-port`/`--control-addr` with `INTERSEPTOR_PROXY_ADDR`
to give a second manually-launched instance its own proxy port too.

For running several projects at once, **`interseptor launcher`** is a small dashboard process
(default `http://127.0.0.1:9965`, no separate auth setup — it auto-generates a local token on start)
that starts/stops per-project Interseptor instances, each its own OS process with its own
auto-allocated control+proxy ports, sharing only the global CA and Starlark checks. Closing the
launcher does not stop the instances it spawned.

**`interseptor stop`** gracefully stops all running Interseptor instances (SIGINT/SIGTERM, waiting
up to a `--timeout`, default 6s); add `--force`/`-f` to force-kill immediately instead.

