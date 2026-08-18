---
layout: default
title: CLI reference
classification: reference
source: docs/cli-reference.md
---
<p class="eyebrow">REFERENCE</p>
# CLI reference

## Main server

```text
interseptor [flags]
```

| Flag or environment variable | Meaning |
|---|---|
| `--project <name|path>` / `INTERSEPTOR_PROJECT` | Open a named project or an explicit project folder. |
| `--open` / `INTERSEPTOR_OPEN_BROWSER` | Open the control UI after startup. |
| `INTERSEPTOR_NO_BROWSER` | Disable browser opening even when another option enables it. |
| `--control-port <port>` | Bind the control UI/API to that port on loopback. |
| `--control-addr <host:port>` / `INTERSEPTOR_CONTROL_ADDR` | Set the complete control address; overrides `--control-port`. |
| `--proxy-port <port>` | Bind one loopback proxy listener to that port. |
| `INTERSEPTOR_PROXY_ADDR` | Override proxy listener address configuration for this launch. |
| `--data-dir <path>` / `INTERSEPTOR_DATA_DIR` | Set the global data directory. |
| `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` | Refuse non-loopback proxy, control, launcher, and related binds. |
| `INTERSEPTOR_NO_UPDATE_CHECK` | Disable the startup update check. |

Underscore aliases such as `--control_port` are accepted for compatibility, but hyphenated names are
canonical.

## Launcher

`interseptor launcher --addr 127.0.0.1:9965` opens the multi-project dashboard. Each started project
runs in its own process with allocated proxy and control ports. Closing the launcher does not stop
those processes.

## Stop and version

```text
interseptor stop [--timeout 6s] [--force|-f]
interseptor version
```

Stop first requests graceful shutdown, then force-kills processes that exceed the timeout. Use
`--force` only when preserving in-flight work is not important.

## Update

```text
interseptor update
interseptor update --check
interseptor update --version v1.2.3
interseptor update --force
```

Update prefers a release binary and verifies published checksums when present; otherwise it falls
back to `go install`.

## MCP

`interseptor mcp` runs the MCP server on stdio and connects to the control URL in
`INTERSEPTOR_CONTROL_URL` (default `http://127.0.0.1:9966`). The running application also exposes
Streamable HTTP at `/mcp`. See [API and MCP]({{ "/api-and-mcp/" | relative_url }}).

## Check authoring

```text
interseptor check new <id>
interseptor check validate [files...]
interseptor check lint [files...]
interseptor check test <file> --flow-json <file|->
```

Checks live under the data directory's `checks/`. Validation compiles passive checks without starting
the server. Test consumes the documented flow JSON shape from a file or standard input.

## Rule packs

```text
interseptor rules create --name <name> [--version <version>] [--out <file>] <dir>
interseptor rules install <pack>
interseptor rules list
interseptor rules info <pack>
interseptor rules remove <pack>
```

Use [Rule packs]({{ "/rule-packs/" | relative_url }}) for manifest, signing, and installation behavior.

## Vault

```text
interseptor vault [--dir <path>] [--addr 127.0.0.1:9977] [--keep 10]
```

`INTERSEPTOR_VAULT_DIR` sets the archive directory. See [Project vault]({{ "/vault/" | relative_url }}) before exposing it
through Tailscale Serve or another network layer.

