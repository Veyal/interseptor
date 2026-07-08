# Interceptor

[![CI](https://github.com/Veyal/interceptor/actions/workflows/ci.yml/badge.svg)](https://github.com/Veyal/interceptor/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Veyal/interceptor?sort=semver)](https://github.com/Veyal/interceptor/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)

A lightweight intercepting **HTTP/HTTPS proxy** and security-testing toolkit — a leaner, native
alternative to Burp Suite, in a **single static Go binary**. It runs a MITM proxy plus a local web
UI, with performance as a first-class constraint: a compiled core, request/response bodies streamed
to disk (never held whole in memory), and passive analysis kept off the hot path.

**Built for the AI-assisted penetration tester.** The human drives a fast web UI; their AI assistant
drives the *same engine* through a real **MCP server** and a **REST/SSE API** — listing and reading
flows, replaying and mutating requests, fuzzing, scanning, and toggling intercept, all under the
tester's direction and **entirely on the local machine**.

> ⚖️ **Responsible use.** Interceptor is intended for testing systems you **own or are explicitly
> authorized to test**. Intercepting other people's traffic or attacking systems without permission
> is illegal in most jurisdictions. You alone are responsible for how you use it.

---

## Contents

[Features](#features) · [Install](#install) · [Quick start](#quick-start) ·
[Intercepting HTTPS](#intercepting-https) · [Configuration](#configuration) ·
[Running multiple projects](#running-multiple-projects) ·
[Security model](#security-model) · [Drive it with AI (MCP)](#drive-it-with-ai-mcp) ·
[Control API](#control-api) · [Architecture](#architecture) · [Development](#development) ·
[Roadmap](#roadmap) · [License](#license)

## Features

- **Intercepting proxy** for HTTP **and** HTTPS, with on-the-fly TLS interception via a local CA
  (per-host leaf certs minted on demand).
- **Live history** — every flow captured (metadata in SQLite, bodies content-addressed on disk),
  filterable/searchable, with a raw/pretty request & response inspector and right-click filters.
- **Intercept workflow** — hold / forward (with edits) / drop **requests *and* responses**, plus
  ordered **match-&-replace** rules.
- **Repeater** — multi-tab; re-send any request, edit it freely, inspect the response, per-tab history.
- **Intruder** — Sniper / Pitchfork (one payload list per `§` marker) / **Race** (no-payload concurrent
  resends for race conditions), with thread + delay controls, payload processing (url/base64/…),
  **grep-match/extract**, anomaly flagging, attack tabs and run history.
- **Authorization testing** — replay a request as each saved identity (role) and diff for broken
  access control (IDOR). **OOB interaction catcher** for blind SSRF/XXE/SQLi/RCE (off by default — remote targets cannot reach `localhost`; enable in Settings → Scanner when you have a tunnel or public URL).
- **Autonomous AI pentesting ("Autopilot")** — reads captured history, plans and runs **active**
  testing autonomously via Interceptor's own tools, and files **only** machine-verified findings
  through a 4-gate verifier (differential reproduction → adversarial verifier agent → out-of-band
  proof for blind classes → human confirm for Critical/High). Every step lands in Activity with the
  request visible in History — nothing is a black box.
- **Active scanning** — a deterministic active-scan engine (`active_scan`) fires per-class payloads
  at a request's injection points and confirms hits with detectors, independent of Autopilot; extend
  it with your own **active** Starlark checks the same way as passive ones.
- **Mobile device support** — Android (adb-based CA install + proxy config) and iOS (profile-based,
  including jailbroken-device SSH automation) setup for HTTPS interception on real devices.
- **Collaboration & remote access** — scoped/expiring API keys, a key-authorized remote-access mode,
  browser login, one-click Cloudflare tunnel, and additive project pull/push merge for two operators
  sharing a target.
- **Multi-project launcher** — `interceptor launcher` runs a small dashboard that starts/stops
  multiple project instances from one place.
- **AI assist** — BYO-key LLM explains requests, suggests payloads (with Repeater/Intruder routing),
  and summarizes findings; streamed, rendered as Markdown.
- **Scanner** — 12 passive checks (missing CSP/HSTS/`nosniff`/clickjacking headers, wildcard CORS,
  reflected parameters, secrets in bodies, insecure cookies, Basic-auth & version disclosure, …),
  exportable as a **Markdown findings report**.
- **Custom checks** — extend the scanner with your own passive checks in sandboxed **Starlark**
  (drop a `.star` file in `~/.interceptor/checks/`). See the
  [check-authoring guide](docs/custom-checks.md) and [`examples/checks/`](examples/checks/).
- **Target scope** — include/exclude rules that focus history, the intercept gate, and the scanner.
- **WebSocket** capture (`ws://`/`wss://` per-frame) **and replay** (a WebSocket Repeater).
- **Session / auth injection** — auto-apply an `Authorization`/`Cookie` to every Repeater & Intruder
  send, plus a **token macro** (CSRF/re-auth: fetch a value from a refresh request, inject per send)
  and a **login macro** (record a login flow, refresh session headers, auto re-auth on 401).
- **Import / export** — HAR in and out, plus portable **project** bundles (flows + rules + scope +
  settings).
- **BYO-key AI assist** — explain a request, suggest payloads, or summarize findings via your own
  **Anthropic** or **OpenRouter** key (off until you set one; the exchange is sent only on request).
- **API & MCP** — a REST control API + SSE event stream and a full **Model Context Protocol** server
  (83 tools, stdio **and** Streamable-HTTP) so an agent or script drives the same core as the UI.

## Install

Interceptor is a single static binary — **no cgo, no Node, no runtime dependencies**.

### Recommended — `go install` (uses the release tags)

Requires **Go 1.25+**:

```bash
# latest release:
go install github.com/Veyal/interceptor/cmd/interceptor@latest
# …or pin a specific release:
go install github.com/Veyal/interceptor/cmd/interceptor@v0.1.0

interceptor        # if $(go env GOPATH)/bin is on your PATH
```

**Or update in place** (no `go install` to remember):

```bash
interceptor update              # latest release
interceptor update --check      # is a newer version out?
interceptor update --version 0.6.0
```

`interceptor update` downloads a prebuilt binary from [GitHub Releases](https://github.com/Veyal/interceptor/releases) when one is attached for your OS/arch (and verifies `checksums.txt` when present). If the release has no binary yet, it falls back to `go install` automatically.

Every tagged version is listed on the [**Releases**](https://github.com/Veyal/interceptor/releases)
page with its changelog; `@latest` resolves to the newest tag, `@vX.Y.Z` pins one.

### From source

```bash
git clone https://github.com/Veyal/interceptor.git
cd interceptor
CGO_ENABLED=0 go build -o interceptor ./cmd/interceptor
./interceptor
```

### Prebuilt binaries

Each tagged release attaches static binaries for **linux / macOS / windows** (amd64 & arm64) plus a
`checksums.txt` on the [Releases](https://github.com/Veyal/interceptor/releases) page (built by the
release workflow when a `v*` tag is pushed) —
download, verify the checksum, `chmod +x`, and run. (`go install` above is equivalent and always
tracks the latest release.)

## Quick start

1. **Run it.** `interceptor` starts the proxy on `127.0.0.1:8080` and the UI on `127.0.0.1:9966`.
   Open that URL in your browser — or start with `--open` to launch it automatically.
2. **Send traffic through it.** Point your browser/HTTP client (or the OS proxy via **Settings →
   System proxy** on macOS) at `127.0.0.1:8080`.
3. **For HTTPS, trust the CA** (see below) — then HTTPS flows are decrypted and editable.
4. **Work the loop.** Watch flows land in **Proxy**, send one to **Repeater** or **Intruder**, run
   the **Scanner**, set **Scope**, or flip on **Intercept** to hold/edit requests and responses.

Runtime data lives under `~/.interceptor/` (`interceptor.db`, `bodies/`, `ca/`). Delete that
directory to reset.

## Intercepting HTTPS

1. Point your client at the proxy (`127.0.0.1:8080`).
2. Download the CA from the **Settings** tab (or `http://127.0.0.1:9966/api/ca.crt`) and
   install/trust it in your OS/browser trust store.
3. HTTPS flows are now decrypted, captured, and editable. Per-host leaf certs are minted on demand
   and cached.

## Configuration

| Environment variable | Effect |
|---|---|
| `INTERCEPTOR_OPEN_BROWSER` | Auto-open the UI on start (same as `--open`). The default is **not** to open it. |
| `INTERCEPTOR_NO_BROWSER` | Hard-disable browser auto-open, overriding `--open`/`INTERCEPTOR_OPEN_BROWSER`. |
| `INTERCEPTOR_ALLOW_EXTERNAL_BIND` | Lock down to **loopback-only** binds when set to `0`/`false`. External bind (e.g. `0.0.0.0` for LAN capture) is allowed by default — see [Security model](#security-model). |
| `INTERCEPTOR_CONTROL_URL` | For `interceptor mcp`: the control API to drive (default `http://127.0.0.1:9966`). |
| `INTERCEPTOR_CONTROL_ADDR` | Env equivalent of `--control-addr`: full control UI/API listen address (`host:port`). |
| `INTERCEPTOR_PROJECT` | Env equivalent of `--project`: open a specific project by name/path, skipping the picker. |
| `INTERCEPTOR_PROXY_ADDR` | Override the proxy listen address(es) (also how the launcher gives each spawned instance its own port). |
| `INTERCEPTOR_NO_UPDATE_CHECK` | Disable the background update check Interceptor runs on every startup. |
| `ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` | Optional fallback key for AI assist when none is set in **Settings → AI**. |
| `GLM_API_KEY` / `ZAI_API_KEY` | Optional fallback key for the GLM/Zhipu AI-assist provider (same tier as `ANTHROPIC_API_KEY`/`OPENROUTER_API_KEY`); `GLM_API_KEY` wins if both are set. |
| `GITHUB_TOKEN` / `INTERCEPTOR_GITHUB_TOKEN` / `GH_TOKEN` | Raises the GitHub API rate limit used for update checks (first non-empty wins). |

The proxy bind address is also runtime-configurable in **Settings** (and persisted).

## Running multiple projects

For one-off multi-instance use, `interceptor` takes root flags: `--project <name|path>` (or
`INTERCEPTOR_PROJECT`) opens a specific project and skips the startup picker; `--control-port <port>`
picks the control UI/API port on loopback (default `9966`); `--control-addr host:port` sets the full
control listen address and overrides `--control-port` (or `INTERCEPTOR_CONTROL_ADDR` — see
[Configuration](#configuration)). Pair `--control-port`/`--control-addr` with `INTERCEPTOR_PROXY_ADDR`
to give a second manually-launched instance its own proxy port too.

For running several projects at once, **`interceptor launcher`** is a small dashboard process
(default `http://127.0.0.1:9965`, no separate auth setup — it auto-generates a local token on start)
that starts/stops per-project Interceptor instances, each its own OS process with its own
auto-allocated control+proxy ports, sharing only the global CA and Starlark checks. Closing the
launcher does not stop the instances it spawned.

**`interceptor stop`** gracefully stops all running Interceptor instances (SIGINT/SIGTERM, waiting
up to a `--timeout`, default 6s); add `--force`/`-f` to force-kill immediately instead.

## Security model

The control plane has **two trust modes** (`internal/control/guard.go`):

- **Loopback trust (default, unchanged).** Both listeners bind **loopback** by default. A request
  that arrives on a loopback connection with a loopback `Host` and no API key is allowed — this is
  how the embedded UI, curl, and the in-process MCP tool bus reach it. The control plane additionally
  **rejects any request with a non-loopback `Host` header or a non-loopback `Origin`**, so a web page
  you happen to visit can't quietly drive the API (CSRF) or read your captured traffic via
  DNS-rebinding. Rebinding the **proxy** or **control UI** to a non-loopback address (e.g. `0.0.0.0`
  for LAN device capture) is allowed from Settings; set `INTERCEPTOR_ALLOW_EXTERNAL_BIND=0` to refuse
  non-loopback binds.
- **Key-authorized remote access (opt-in, added in v0.29.0).** A request carrying a valid API key is
  authorized regardless of Host/Origin/connection — this is what lets an AI agent on a VPS or a
  collaborator's browser reach Interceptor over a tunnel. Keys are **scoped**: a **read**-only key may
  only view (GET/HEAD + the SSE stream); a **full** key may also mutate. Browser access goes through
  `/login`, which mints an httpOnly session cookie; cookie-authenticated mutations additionally
  require an anti-CSRF header and a same-origin `Origin`, since a cookie is an ambient credential (the
  bearer-token path has no such requirement, since a bearer token isn't ambient). The `/mcp` endpoint
  always requires a **full**-scope key when any key exists. A non-loopback request with no valid key
  is closed outright (401, or redirected to `/login` for a browser navigation) — so accidentally
  exposing the port never leaks captured pentest data. The optional **Cloudflare quick tunnel**
  (Settings → API & MCP → Share) is opt-in and **refuses to start unless at least one API key already
  exists**, so the tunnel can never expose an unauthenticated instance.

Captured traffic and any AI key never leave your machine except on an explicit AI-assist request to
your chosen provider, or traffic you deliberately expose via the remote-access mode above.

**Data at rest is unencrypted.** Captured requests/responses — which can include credentials, session
tokens, and other PII from whatever you're testing — are stored **unencrypted** under `~/.interceptor/`
(SQLite database + content-addressed body files). Interceptor does not encrypt this data at rest;
securing the machine and disk it runs on is the operator's responsibility.

## Drive it with AI (MCP)

Interceptor ships a **Model Context Protocol** server so an AI assistant can operate the proxy with
the same capabilities as the UI. Run the app, then connect your MCP client one of two ways:

**stdio** (Claude Desktop / Claude Code) — point your client at the `mcp` subcommand:

```jsonc
{
  "mcpServers": {
    "interceptor": { "command": "interceptor", "args": ["mcp"] }
  }
}
```

**Streamable-HTTP** (hosted/remote agents) — `POST` JSON-RPC to `http://127.0.0.1:9966/mcp`
(stateless; no subprocess needed).

Both expose the same **83 tools** — reading flows (`list_flows`, `get_flow`, `analyze_flow`,
`flow_as_curl`), replaying/fuzzing (`send_request`, `start_intruder`, `ws_send`), scanning
(`run_scanner`, `scan_report`), intercept/rules/scope control, and `set_session` — with bounded
results so large bodies don't blow the agent's context. The **Settings → API & MCP** section shows
a copy-paste config and the live tool list.

## Control API

The full REST surface is documented at runtime: `GET /api/reference` (or the **Settings → API & MCP** section).
Live updates stream over Server-Sent Events at `GET /api/events`. Highlights: `/api/flows`,
`/api/repeater/send`, `/api/intruder/start`, `/api/scanner/run`, `/api/scope`, `/api/session`,
`/api/ws/send`, `/api/export/{har,project}`, `/api/settings`.

## Architecture

One Go binary, two localhost listeners. Each `internal/*` package has a single responsibility and is
independently tested.

| Package | Responsibility |
|---|---|
| `internal/store` | SQLite metadata (flows, rules, settings, issues, ws frames, scope, views, keys) + content-addressed body files |
| `internal/capture` | Stream bodies to the store via `io.TeeReader` (never buffered whole) |
| `internal/tlsca` | Local CA: load/generate, mint + cache per-host leaf certificates |
| `internal/intercept` | Hold queue (forward/edit/drop) for requests **and** responses + match-&-replace |
| `internal/proxy` | Forward proxy, `CONNECT` + TLS MITM, WebSocket frame relay, flow capture, upstream proxy |
| `internal/scope` | Target-scope include/exclude matcher (host wildcards + path prefixes) |
| `internal/sender` | One-off direct request sender (+ session headers, CSRF/re-auth token macro, authz replays) — backs Repeater & Intruder |
| `internal/intruder` | Sniper / Pitchfork / Race attack engine (threads, delay, grep-match/extract, payload processing) |
| `internal/scanner` | Passive security checks over captured flows |
| `internal/activescan` | Deterministic **active**-scan engine: enumerates a request's injection points, fires per-class payloads through a caller-supplied sender, confirms with detectors (distinct from the passive `internal/scanner`) |
| `internal/activescript` | Runs user-authored **active** scanner checks in Starlark — the active twin of `internal/checkscript` |
| `internal/oob` | Out-of-band interaction catcher (blind SSRF/XXE/SQLi/RCE callbacks) |
| `internal/checkscript` | Runs user-authored Starlark scanner checks (sandboxed, bounded) |
| `internal/curlgen` · `internal/report` | Render a flow as `curl`; render findings as Markdown |
| `internal/wsrepeater` | WebSocket Repeater (RFC 6455 handshake + masked frames, no deps) |
| `internal/harx` | HAR 1.2 import/export |
| `internal/sysproxy` | Opt-in macOS system-proxy toggle |
| `internal/aiassist` | BYO-key LLM bridge (Anthropic + OpenRouter + GLM) |
| `internal/aiagent` | Provider-agnostic, budgeted tool-calling agent loop that powers Autopilot's planning and adversarial-verifier agents |
| `internal/autopwn` | Autonomous-pentest ("Autopilot") run engine: plans and executes active testing over Interceptor's own tools, files only findings proven by the 4-gate verifier |
| `internal/verify` | Deterministic, LLM-free primitives (differential reproduction, OOB-callback confirmation) behind Gates 1 and 3 of Autopilot's 4-gate finding verifier |
| `internal/android` | Configures a USB-connected Android device for HTTPS interception via `adb` (CA install + proxy config) |
| `internal/ios` | Configures iOS simulators (via `simctl`) and physical devices (`.mobileconfig` profile, or SSH automation for jailbroken devices) for HTTPS interception |
| `internal/tunnel` | Manages a Cloudflare quick tunnel (`cloudflared` child process) exposing the control plane at a public `https://*.trycloudflare.com` URL |
| `internal/launcher` | Disk-backed registry (`~/.interceptor/instances.json`) of running per-project instances + port allocation, backing the `interceptor launcher` dashboard (`cmd/interceptor/launcher.go`) |
| `internal/codec` | Pure encode/decode transforms (base64, URL, hex, HTML entities, JWT inspection, smart auto-decode) behind the Decoder tool and MCP `decode` |
| `internal/auth/jwtextract` | Pulls JWT-shaped tokens out of flows (header/JSON/query/cookie) for cross-host token replay and SSO authz testing |
| `internal/mcp` | MCP server (stdio + Streamable-HTTP) over the control API |
| `internal/control` | REST + SSE API, security guard, serves the embedded web UI |
| `cmd/interceptor` | Config, wiring, lifecycle (both listeners, runtime rebind, graceful shutdown) |

The web UI lives in `internal/control/ui/` (embedded via `//go:embed`): an `index.html` shell,
`app.css`, and native ES modules under `js/` — `core.js` (shared foundation) plus one module per
feature, wired together by `app.js`. No build step or bundler; the binary stays single and static.
Design notes and per-slice specs/plans live under [`docs/`](docs/).

## Development

```bash
go test ./...          # all tests
go test -race ./...    # race detector (must be clean)
go vet ./...           # static checks (must be clean)
```

Please read [CONTRIBUTING.md](CONTRIBUTING.md) before sending changes — **TDD**, no cgo,
[Conventional Commits](https://www.conventionalcommits.org/), and a [CHANGELOG.md](CHANGELOG.md)
entry per change are expected.

## Roadmap

Why this exists, who it's for, and what's next live under [`docs/product/`](docs/product/):
[strategy](docs/product/strategy.md) · [roadmap](docs/product/roadmap.md) ·
[metrics](docs/product/metrics.md) · [MCP cookbook](docs/product/mcp-cookbook.md) ·
[flagship PRD](docs/product/prd-0001-target-scope.md).
Performance numbers (≈20 MB idle, ≈1 s cold start) are in [docs/benchmarks.md](docs/benchmarks.md).
Larger bets ahead: login-macro/401 re-auth session handling, HTTP/2, an extension API, and automated
release binaries for every platform.

## License

[MIT](LICENSE) © 2026 veyal.
