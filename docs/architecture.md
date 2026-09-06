# Architecture

## Security model

The control plane has **two trust modes** (`internal/control/guard.go`):

- **Loopback trust (default, unchanged).** Both listeners bind **loopback** by default. A request
  that arrives on a loopback connection with a loopback `Host` and no API key is allowed — this is
  how the embedded UI, curl, and the in-process MCP tool bus reach it. The control plane additionally
  **rejects any request with a non-loopback `Host` header or a non-loopback `Origin`**, so a web page
  you happen to visit can't quietly drive the API (CSRF) or read your captured traffic via
  DNS-rebinding. Rebinding the **proxy** or **control UI** to a non-loopback address (e.g. `0.0.0.0`
  for LAN device capture) is allowed from Settings; set `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` to refuse
  non-loopback binds.
- **Non-loopback proxy authentication.** A proxy listener bound to a non-loopback interface returns
  `407 Proxy Authentication Required` with Basic realm `interseptor` until the client supplies any
  username and a valid **full-scope** API key as the password. Read-only keys cannot proxy because
  forwarding and capture mutate state. The listener strips `Proxy-Authorization` before forwarding.
  Loopback listeners do not require this credential. See [Proxy authentication](proxy-and-tls.md#proxy-authentication).
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

Captured traffic never leaves your machine unless you deliberately expose it through remote access or an external MCP/REST client. Interseptor has no built-in model provider and stores no provider keys.


**Data at rest is unencrypted.** Captured requests/responses — which can include credentials, session
tokens, and other PII from whatever you're testing — are stored **unencrypted** under `~/.interseptor/`
(`interseptor.db` SQLite database + content-addressed body files). Interseptor does not encrypt this data at rest;
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
| `internal/oob` | Out-of-band interaction catcher (blind SSRF/XXE/SQLi/RCE callbacks) |
| `internal/checkscript` | Runs user-authored Starlark scanner checks (sandboxed, bounded) |
| `internal/msgcodec` | Project-scoped Starlark message codecs (app-layer encrypt/decrypt for History/Repeater; never on the proxy hot path) |
| `internal/curlgen` · `internal/report` | Render a flow as `curl`; render findings as Markdown |
| `internal/wsrepeater` | WebSocket Repeater (RFC 6455 handshake + masked frames, no deps) |
| `internal/harx` | HAR 1.2 import/export |
| `internal/sysproxy` | Opt-in macOS system-proxy toggle |
| `internal/verify` | Deterministic verification primitives for replay and evidence workflows |
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
`app.css`, and native ES modules under `js/`. `core.js` owns shared UI primitives, `motion.js` owns
state-driven motion, and `project.js` owns active-project readiness plus the one lazy Map loader
shared by main navigation and Proxy's **Search in Map** action. Feature behavior stays in its
feature module; `app.js` owns global navigation, shortcuts, SSE dispatch, and boot. No build step or
bundler; the binary stays single and static.

Workspace startup has two independent safety boundaries. A small classic-script guard runs before
the ES-module graph and replaces the static loading state with a Reload action if those modules
cannot load or evaluate. Once `app.js` is running, project identity and Repeater/Intruder state
hydration race their requests against real settling deadlines; `AbortController` is used for
cleanup, not as the promise-settlement guarantee. A failed saved-state read unlocks the workstation
with its browser-local drafts intact, while an unresolved project identity keeps project-scoped
tools locked rather than selecting a guessed project. The pre-module lock is reversible if a slow
module eventually completes. A terminal startup alert clears navigation and panel `aria-busy`
without enabling unavailable controls, so assistive technology receives a settled failure rather
than an indefinite loading state. Browser-local tab envelopes are schema-checked and bounded before
normalization or rendering; unsafe values remain available for recovery and cannot be replaced by
the automatic blank-tab write. All Repeater creation routes share the same reload-safe tab and ID
limits. Browser storage keys use the versioned, percent-encoded canonical project directory:
uniquely owned localStorage and IndexedDB history keys migrate once, while an ambiguous older key is
left intact and reported instead of being assigned to the wrong project. If only the canonical
directory is available while project-list ownership is unknown, migration or removal of an affected
unscoped or name-keyed legacy value is deferred. An unscoped value also stays untouched when several
projects make its owner ambiguous, including duplicate display names; malformed or blank project-list
entries never count as ownership proof. New edits still use the collision-free canonical-directory browser
key and continue synchronizing with the project database. Client-side persistence
uses the project API's 4 MiB UTF-8 byte limit and retains larger browser drafts without retrying a
request the server cannot accept. A browser-local write failure is visible but does not suppress the
project-database synchronization path. Replacing an ignored malformed pending marker with a valid
explicit edit immediately re-enables that synchronization path.

The [UI motion specification](ui-motion-spec.md) owns motion behavior and constraints. Other design
notes and per-slice specs/plans live under [`docs/`](.).

Workspace controls use app-rendered dropdowns, checkbox/radio appearances,
disclosure indicators, confirmation dialogs and contextual hints. Hidden select
elements remain only as value/change adapters for the existing feature modules;
they cannot paint a native menu, including before module startup. `hints.js`
converts static and dynamically assigned title hints into one themed surface.
Settings use a flat reading order and expandable reference text; state, errors and
material consequences remain visible. File access continues through explicit
import/export actions.
