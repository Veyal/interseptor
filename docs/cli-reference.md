# CLI reference

## Main server

```text
interseptor [flags]
```

| Flag or environment variable | Meaning |
|---|---|
| `--project <name\|path>` / `INTERSEPTOR_PROJECT` | Open a named project or an explicit project folder. |
| `--headless` / `INTERSEPTOR_HEADLESS` | Don't open the UI window on start; the UI URL is logged instead. |
| `--open` / `INTERSEPTOR_OPEN_BROWSER` | Open the UI window even when started over SSH. |
| `INTERSEPTOR_NO_BROWSER` | Never open the UI window, even when another option enables it. |
| `--control-port <port>` | Bind the control UI/API to that port on loopback. |
| `--control-addr <host:port>` / `INTERSEPTOR_CONTROL_ADDR` | Set the complete control address; overrides `--control-port`. |
| `--proxy-port <port>` | Bind one loopback proxy listener to that port. |
| `INTERSEPTOR_PROXY_ADDR` | Override proxy listener address configuration for this launch. |
| `--data-dir <path>` / `INTERSEPTOR_DATA_DIR` | Set the global data directory. |
| `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` | Refuse non-loopback proxy, control, launcher, and related binds. |
| `INTERSEPTOR_NO_UPDATE_CHECK` | Disable the startup update check. |
| `INTERSEPTOR_TRUSTED_PROXY_HOSTS` | Comma-separated hostnames of a reverse proxy or named tunnel whose `X-Forwarded-For` / `CF-Connecting-IP` / `X-Real-IP` count for the IP allowlist. A request that carries a forwarding header (`X-Forwarded-*`, `Forwarded`, `X-Real-IP`, `CF-Connecting-IP`, `CF-Ray`, `True-Client-IP`, `Tailscale-*`) never takes the unauthenticated loopback path, whatever its `Host`: loopback trust requires a loopback peer and a loopback `Host` with no forwarder in between, because a forwarder that keeps the client `Host` (Tailscale Serve over HTTPS, a catch-all reverse proxy) would otherwise relay a remote client straight into keyless access. |

Underscore aliases such as `--control_port` are accepted for compatibility, but hyphenated names are
canonical.

## Launcher

`interseptor launcher --addr 127.0.0.1:9965` opens the multi-project dashboard. Each started project
runs in its own process with allocated proxy and control ports. Closing the launcher does not stop
those processes.

## Stop and version

```text
interseptor stop [--timeout 6s] [--force|-f] [--all]
interseptor version
```

Stop first requests graceful shutdown, then force-kills processes that exceed the timeout. Use
`--force` only when preserving in-flight work is not important. By default every proxy server
instance is stopped and `interseptor mcp` bridges, the launcher and the vault are left running (and
counted in the output); `--all` stops them as well.

## Update

```text
interseptor update
interseptor update --check
interseptor update --version v1.2.3
interseptor update --force
```

Update prefers a release binary and verifies published checksums when present; otherwise it falls
back to resolving that release tag's commit and building from source with `go` (or prints `git clone` build commands).

## Install the macOS app

```text
interseptor install-desktop [--dir <path>] [--version <x.y.z>] [--force] [--open]
```

macOS only. Downloads `Interseptor-v<version>-unsigned.zip` for the latest stable release (or
`--version`) from GitHub, checks its SHA-256 against the release's `macos-checksums.txt`, and installs
`Interseptor.app` into `/Applications` (or `~/Applications` when `/Applications` is not writable, or
the absolute `--dir`). Before the swap it checks the bundle identifier, the version and the code
signature (`codesign --verify --deep --strict`). The app is swapped in atomically, and the previous
copy is restored if the swap fails. It refuses while that app is running. It does nothing when the
same version is already installed, and it will not replace a newer version unless you pass `--force`.
`--open` launches the app afterwards. Because the CLI downloads the zip itself, the app carries no
quarantine flag and opens without the Gatekeeper **Open Anyway** step.

## Install Chrome

```text
interseptor install-chrome [--data-dir <path>] [--force]
```

Installs Chrome for Testing under the data directory. The CLI uses it for its UI window, PDF report
export and the test browser when no installed Chrome, Edge, Chromium or Brave is found. The macOS
desktop app does not need it.

## MCP

`interseptor mcp` runs the MCP server on stdio and connects to the control URL in
`INTERSEPTOR_CONTROL_URL` (default `http://127.0.0.1:9966`). The running application also exposes
Streamable HTTP at `/mcp`. See [API and MCP](api-and-mcp.md).

| Flag or environment variable | Meaning |
|---|---|
| `INTERSEPTOR_CONTROL_URL` | Control plane to drive (default `http://127.0.0.1:9966`). |
| `INTERSEPTOR_MCP_PROFILE` | Which subset of the tool surface to serve: `full` (the default, every tool), `web-pentest`, `reporting` or `mobile`. Also the default for the HTTP `/mcp` endpoint, which takes `?profile=` per request. An unrecognised value falls back to `full` for stdio and is a `400` over HTTP. |

## Vault

```text
interseptor vault [--dir <path>] [--addr 127.0.0.1:9977] [--keep 10]
```

`INTERSEPTOR_VAULT_DIR` sets the archive directory. See [Project vault](vault.md) before exposing it
through Tailscale Serve or another network layer.

## Copy Abilities

`interseptor ability` (alias `interseptor abilities`) manages global and project-scoped [Copy Abilities](abilities.md) locally without starting the server.

```text
interseptor ability <subcommand> [options]
```

| Subcommand | Meaning |
|---|---|
| `list [--json]` | List installed abilities (`ID`, `SCOPE`, `VERSION`, `STATUS`, `NAME`, `DIGEST`), or emit `{"abilities": [...]}` with `--json`. |
| `install <archive-or-dir> [--scope global\|project]` | Install a `.tar.gz`, `.zip`, or local folder through the atomic quarantine pipeline (always starts `quarantined`). |
| `trust <id> [--enable=true\|false]` | Approve an ability's SHA-256 digest in the machine-owned trust store and enable it (`--enable` defaults to `true`). |
| `enable <id>` | Enable an already-trusted, non-drifted ability. |
| `disable <id>` | Disable an ability without revoking its trusted digest. |
| `scaffold <id> [flags]` | Create a starter ability directory (`ability.json`, `ui/*`, `hooks/*`), installed quarantined. Flags: `--name`, `--group` (`capture\|test\|recon\|report\|configure\|abilities`, default `abilities`), `--desc` (`--description`), `--author`, `--scope`. |
| `export <id> [-o <path>\|--out <path>]` | Export an ability as a deterministic `.tar.gz` archive (defaults to `<id>.tar.gz`; `-` writes to stdout). |
| `remove <id>` | Delete an ability directory and clear its trust record. |

Common options (accepted before or interleaved with subcommand arguments):

| Flag or environment variable | Meaning |
|---|---|
| `--data-dir <path>` / `INTERSEPTOR_DATA_DIR` | Global data directory (default `~/.interseptor`; `--data_dir` also accepted before the subcommand). |
| `--project <name\|dir>` / `INTERSEPTOR_PROJECT` | Project name or folder (`default` selects the global data directory). |
| `--scope <global\|project>` | Target scope for `install` and `scaffold` (defaults to `project` when a named/external `--project` is set, else `global`; `--scope project` requires `--project` and is unavailable on `default`). |
