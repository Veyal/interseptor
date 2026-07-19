# Architecture

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
  collaborator's browser reach Interseptor over a tunnel. Keys are **scoped**: a **read**-only key may
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
tokens, and other PII from whatever you're testing — are stored **unencrypted** under `~/.interseptor/`
(SQLite database + content-addressed body files). Interseptor does not encrypt this data at rest;
securing the machine and disk it runs on is the operator's responsibility.

For the vulnerability-reporting policy (bugs *in* Interseptor itself), see [SECURITY.md](../SECURITY.md).

## Package layout

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
| `internal/msgcodec` | Project-scoped Starlark message codecs (app-layer encrypt/decrypt for History/Repeater; never on the proxy hot path) |
| `internal/curlgen` · `internal/report` | Render a flow as `curl`; render findings as Markdown |
| `internal/wsrepeater` | WebSocket Repeater (RFC 6455 handshake + masked frames, no deps) |
| `internal/harx` | HAR 1.2 import/export |
| `internal/sysproxy` | Opt-in macOS system-proxy toggle |
| `internal/aiassist` | BYO-key LLM bridge (Anthropic + OpenRouter + GLM/Zhipu + OpenAI) |
| `internal/aiagent` | Provider-agnostic, budgeted tool-calling agent loop that powers Autopilot's planning and adversarial-verifier agents |
| `internal/autopwn` | Autonomous-pentest ("Autopilot") run engine: plans and executes active testing over Interseptor's own tools, files only findings proven by the 4-gate verifier |
| `internal/verify` | Deterministic, LLM-free primitives (differential reproduction, OOB-callback confirmation) behind Gates 1 and 3 of Autopilot's 4-gate finding verifier |
| `internal/android` | Configures a USB-connected Android device for HTTPS interception via `adb` (CA install + proxy config) |
| `internal/ios` | Configures iOS simulators (via `simctl`) and physical devices (`.mobileconfig` profile, or SSH automation for jailbroken devices) for HTTPS interception |
| `internal/tunnel` | Manages a Cloudflare quick tunnel (`cloudflared` child process) exposing the control plane at a public `https://*.trycloudflare.com` URL |
| `internal/launcher` | Disk-backed registry (`~/.interseptor/instances.json`) of running per-project instances + port allocation, backing the `interseptor launcher` dashboard (`cmd/interseptor/launcher.go`) |
| `internal/codec` | Pure encode/decode transforms (base64, URL, hex, HTML entities, JWT inspection, smart auto-decode) behind the Decoder tool and MCP `decode` |
| `internal/auth/jwtextract` | Pulls JWT-shaped tokens out of flows (header/JSON/query/cookie) for cross-host token replay and SSO authz testing |
| `internal/mcp` | MCP server (stdio + Streamable-HTTP) over the control API |
| `internal/control` | REST + SSE API, security guard, serves the embedded web UI |
| `cmd/interseptor` | Config, wiring, lifecycle (both listeners, runtime rebind, graceful shutdown) |

## Web UI

The web UI lives in `internal/control/ui/` (embedded via `//go:embed`): an `index.html` shell,
`app.css`, and native ES modules under `js/` — `core.js` (shared foundation) plus one module per
feature, wired together by `app.js`. No build step or bundler; the binary stays single and static.
Design notes and per-slice specs/plans live under [`docs/`](.).
