# Interceptor

A lightweight intercepting HTTP/HTTPS proxy and security-testing toolkit — a leaner,
native alternative to Burp Suite. Interceptor is a single static Go binary that runs a
MITM proxy plus a local web UI, with **performance as a first-class constraint**: a compiled
core, request/response bodies streamed to disk (never held whole in memory), and passive
analysis kept off the hot path.

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

- **Proxy** — `127.0.0.1:8080` (runtime-configurable in Settings; may bind `0.0.0.0` for LAN/device capture)
- **Control UI + API** — `127.0.0.1:9966`

Set `INTERCEPTOR_NO_BROWSER=1` to suppress auto-opening the browser (headless/server use).
Runtime data lives under `~/.interceptor/` (`interceptor.db`, `bodies/`, `ca/`).

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
