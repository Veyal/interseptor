# Interceptor

A lightweight intercepting HTTP/HTTPS proxy and security-testing toolkit — a leaner,
native alternative to Burp Suite. Interceptor is a single static Go binary that runs a
MITM proxy plus a local web UI, with **performance as a first-class constraint**: a compiled
core, request/response bodies streamed to disk (never held whole in memory), and passive
analysis kept off the hot path.

**Built for the AI-assisted penetration tester.** The human drives a fast web UI; their AI assistant
drives the **same engine** through a real **MCP server** (`interceptor mcp`) and a **REST/SSE API** —
listing and reading flows, replaying and mutating requests, running Intruder/Scanner, toggling
intercept, and adding rules, all under the tester's direction and entirely on the local machine.
See [`docs/product/`](docs/product/) for the product intent and roadmap.

> Status: feature-complete on the core proxy plus the Repeater, Intruder, Scanner, WebSocket
> capture, and API modules. See [CHANGELOG.md](CHANGELOG.md).

## Features

- **Intercepting proxy** for HTTP **and** HTTPS, with on-the-fly TLS interception via a local CA.
- **Live history** — every flow captured (metadata in SQLite, bodies content-addressed on disk),
  filterable/sortable, with a raw/pretty request & response inspector.
- **Intercept workflow** — hold / forward (with edits) / drop requests, plus ordered
  **match-&-replace** rules.
- **Repeater** — re-send any request, edit it freely, inspect the response, keep a history.
- **Intruder** — Sniper / Pitchfork payload attacks over `§…§` fuzz points, with anomaly flagging.
- **Scanner** — passive security checks over captured traffic (missing CSP/HSTS, wildcard CORS,
  secrets in bodies, insecure cookies, version disclosure, …).
- **WebSocket capture** — `ws://` / `wss://` tunnelled transparently with per-frame capture.
- **API** — REST control API + SSE event stream + API keys + an MCP descriptor, all driving the
  same core an agent or script can automate.

## Install & run

Requires **Go 1.25+**. No cgo, no Node — the UI ships embedded in the binary.

```bash
go run ./cmd/interceptor
# or build a static binary:
CGO_ENABLED=0 go build -o interceptor ./cmd/interceptor && ./interceptor
```

On start it listens on two localhost ports and opens the UI in your browser:

- **Proxy** — `127.0.0.1:8080` (runtime-configurable in Settings)
- **Control UI + API** — `127.0.0.1:9966`

Set `INTERCEPTOR_NO_BROWSER=1` to suppress auto-opening the browser (headless/server use).
Runtime data lives under `~/.interceptor/` (`interceptor.db`, `bodies/`, `ca/`).

### Security model

Both listeners bind loopback. The control plane additionally **rejects non-loopback `Host`
headers and cross-origin requests**, so a web page you visit can't drive the API behind your back
(CSRF / DNS-rebinding). Rebinding the **proxy** to a non-loopback address (e.g. `0.0.0.0` to capture
a phone on your LAN) is refused unless you opt in with `INTERCEPTOR_ALLOW_EXTERNAL_BIND=1`.

### Intercepting HTTPS

1. Point your client at the proxy (`127.0.0.1:8080`).
2. Download the CA from the **Settings** tab (or `http://127.0.0.1:9966/api/ca.crt`) and install/trust it.
3. HTTPS flows are now decrypted, captured, and editable. Per-host leaf certs are minted on demand.

## Architecture

One Go binary, two localhost listeners (the proxy is never itself captured; the control plane never
binds externally). Each package has a single responsibility and is independently tested.

| Package | Responsibility |
|---|---|
| `internal/store` | SQLite metadata (flows, rules, settings, issues, ws frames, API keys) + content-addressed body files |
| `internal/capture` | Stream bodies to the store via `io.TeeReader` (never buffered whole) |
| `internal/tlsca` | Local CA: load/generate, mint + cache per-host leaf certificates |
| `internal/intercept` | Hold queue (forward/edit/drop) + request-side match-&-replace engine |
| `internal/proxy` | Forward proxy, `CONNECT` + TLS MITM, WebSocket frame relay, flow capture |
| `internal/sender` | One-off direct request sender (backs Repeater & Intruder) |
| `internal/intruder` | Sniper / Pitchfork attack engine |
| `internal/scanner` | Passive security checks over captured flows |
| `internal/control` | REST + SSE API, serves the embedded web UI |
| `cmd/interceptor` | Config, wiring, lifecycle (both listeners, runtime rebind) |

The web UI is a single self-contained `internal/control/ui/index.html`, embedded via `//go:embed`.

Design notes and the per-slice specs/plans live under [`docs/`](docs/).

## Drive it with AI (MCP)

Interceptor ships a **Model Context Protocol server** so an AI assistant can operate the proxy with
the same capabilities as the UI. Run the app, then point your MCP client at `interceptor mcp`:

```jsonc
// e.g. Claude Desktop / Claude Code MCP config
{
  "mcpServers": {
    "interceptor": { "command": "interceptor", "args": ["mcp"] }
  }
}
```

`interceptor mcp` is a stdio MCP server that drives the running instance over its control API
(override the target with `INTERCEPTOR_CONTROL_URL`). It exposes 20 tools — `list_flows`, `get_flow`, `analyze_flow`,
`send_request`, `start_intruder`, `intruder_state`, `run_scanner`, `list_issues`, `get_intercept`,
`set_intercept`, `set_response_intercept`, `forward_request`, `drop_request`, `list_rules`,
`add_rule`, `list_ws_frames`, `list_scope`, `add_scope_rule`, `get_settings`, `ca_info` — with
bounded results so large bodies don't blow the agent's context.
The UI's **API → MCP** tab shows a copy-paste config and the live tool list. Captured traffic never
leaves your machine — the agent drives the local engine.

## Control API

The full REST surface is documented at runtime: `GET /api/reference` (or the **API → REST** tab).
Live updates are pushed over Server-Sent Events at `GET /api/events`. Highlights:
`/api/flows`, `/api/repeater/send`, `/api/intruder/start`, `/api/scanner/run`, `/api/rules`,
`/api/intercept/*`, `/api/settings`, `/api/keys`.

## Development

```bash
go test ./...          # all tests
go test -race ./...    # race detector
go vet ./...           # static checks
```

Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the coding standards before sending changes —
TDD, no cgo, conventional commits, and a CHANGELOG entry per change are expected.

## Product & roadmap

Why this exists, who it's for, and what's next live under [`docs/product/`](docs/product/):
[strategy](docs/product/strategy.md) · [roadmap](docs/product/roadmap.md) ·
[metrics](docs/product/metrics.md) · [flagship PRD](docs/product/prd-0001-target-scope.md).
Performance numbers (≈20 MB idle, ≈1 s cold start) are in [docs/benchmarks.md](docs/benchmarks.md).
