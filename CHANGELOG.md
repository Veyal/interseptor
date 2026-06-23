# Changelog

All notable changes to **Interceptor** are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.1] — 2026-06-23

### Security
- **Active-scan safety hardening** (from a skeptical review of the v0.2.0 active-scan surface, which
  confirmed the core is safe — no cross-host sends, no redirect-following, non-destructive payloads):
  - **Refuse an “all in-scope” run when no scope include rule is set** — otherwise it would actively
    attack *every* captured host. (Scanning a single selected flow is unaffected.)
  - **Self-target guard now covers both listeners** (control **and** proxy) with loopback
    normalization (`localhost` / `::1` / `127.x`), not just a literal `127.0.0.1:9966` compare.
  - **Closed a TOCTOU in start** — two concurrent starts could both pass the “already running” check
    and launch (orphaning the kill switch); `running` is now checked-and-set under one lock.
  - **The kill switch now aborts in-flight probes** — the scan context is threaded into the sender
    (`req.WithContext`), so Stop cancels running requests instead of waiting out their timeout.

## [0.2.0] — 2026-06-23

Headline: **active scanning** (deterministic + AI-operable), an **extensible scanner** (custom
Starlark checks, authored in-app or by the AI), a **Decoder**, a multi-tab **Repeater**, a redesigned
**Settings**, a **Ctrl/Cmd+K command palette** with keyboard shortcuts, and an `SQLITE_BUSY` fix —
**32 MCP tools** total.

### Added
- **Active scanning — Phase 1 (engine + API + MCP)** — a deterministic active-scan engine
  (`internal/activescan`) that **sends crafted payloads to confirm vulnerabilities** without any AI:
  it enumerates query/form/JSON injection points, fires per-class payloads through the existing
  `sender` (probes recorded + session-auth applied), and confirms with detectors for **reflected XSS,
  error- & boolean-based SQLi, SSTI, open redirect, path traversal, and timing OS-command-injection**.
  **Safety-gated:** a session-level **arm** consent (refuses to run disarmed), **scope-restricted**
  targets, non-destructive payloads, a shared request budget, and a **kill switch** (cancellable run).
  Control: `GET /api/activescan`, `POST /api/activescan/{arm,start,stop}` (`start` takes a `flowId` or
  `inScope`); probes are flagged `FlagActiveScan` and kept out of the proxy history/passive scan.
  MCP: **`active_scan` / `active_scan_state` / `active_scan_stop`** (now **32 tools**) so the AI can
  arm-and-operate the same engine. Findings land in the issues store as `[active] …` with the
  confirming request/response linked. TDD on every detector + engine; verified live against a
  vulnerable target (XSS + SQLi + open-redirect confirmed). Design: [prd-0002](docs/product/prd-0002-active-scanning.md).
- **Active scanning — UI** (Scanner → **⚡ Active scan**, also in the Ctrl/Cmd+K palette): a prominent
  red **arm/consent** banner, target picker (selected flow vs all in-scope), a max-requests cap,
  start/stop, live progress over SSE, and confirmed findings that open the proving request/response.
  The scanner now also **refuses to target its own control plane** (`SelfAddr` guard — relevant if the
  UI is reached through the proxy with the system proxy on). Verified live in a headless browser.
- **Decoder / encoder** — a `🧰 Decoder` tool (open it from the **Ctrl/Cmd+K** palette): Base64,
  URL, hex, HTML-entity, JWT inspection, and a **smart** auto-detect-and-decode, with chain
  (output → next input) and copy. Pure transforms in a tested `internal/codec`, exposed at
  `POST /api/decode` and as an MCP **`decode`** tool (now **29 tools**) so the AI can crack tokens
  and parameters too.
- **In-app custom-check management + AI authoring** — manage Starlark scanner checks without touching
  files. A Scanner-tab **✎ Custom checks** editor lists checks and lets you **new / test against a
  captured flow / save / delete**, backed by `GET /api/checks`, `GET/PUT/DELETE /api/checks/{id}`,
  and `POST /api/checks/test` (compile-validated — a non-compiling check is rejected, never written;
  ids are path-sandboxed). New MCP tools **`list_checks` / `test_check` / `save_check` /
  `delete_check`** (now **28 tools**) let the AI write, test, and save its own scanner checks. TDD on
  the store CRUD + id sandboxing; verified live end-to-end (the AI authored a check that then fired
  in a scan).

### Changed
- **Suppress the browser's native right-click menu** app-wide so the app's own context menu (and a
  cleaner feel) takes over — but it's still allowed in editable fields (paste/cut) and over a live
  text selection (copy), so nothing useful is lost.
- **Settings redesigned** — the long single-scroll Settings page is now a left **sub-nav**
  (Proxy & network · TLS / CA · Target scope · AI assist · Session / auth · Project & data) with a
  focused content pane per section. Regrouped related controls, surfaced the **Download CA** action,
  added a "define scope by right-clicking a flow" tip and a note on the `~/.interceptor/` data dir,
  clarified the external-bind opt-in, and removed the redundant second intercept toggle. Verified in
  a headless browser (0 console errors).

### Added
- **Repeater multi-tab** — the Repeater now holds multiple tabs, one per endpoint under test, each
  with its own request editor, response, and **endpoint-scoped send history**. New tab (＋) / close
  (✕); open tabs (method/URL/headers/body) persist across reloads via `localStorage`. **Send to
  Repeater** focuses the matching endpoint's tab or opens a new one.
- **Command palette (Ctrl / Cmd + K)** — fuzzy-search captured flows, jump to any tab, and run
  commands (toggle intercept, run scanner, in-scope, export HAR, …) from one overlay; arrow-key
  navigation, ⏎ to run, esc to close.
- **Keyboard shortcuts** — **Ctrl+R** send the selected flow to Repeater, **Ctrl+I** to Intruder,
  **Ctrl+Space** send the current Repeater request, **/** focus history search.
- **Smarter history filters** — the method dropdown now lists only the HTTP methods that actually
  appear in the current history (no empty POST/PUT/… options).
- **Define scope from history** — a right-click **Add to scope** action adds a host as an include
  rule, so the **◎ in scope** toggle is one click away from useful.
- **Body beautify (size-gated)** — the inspector's **Pretty** view now pretty-prints JSON (and lightly
  indents HTML/XML), but only for bodies under 256 KB so large responses stay cheap.
- **Custom scanner checks (Starlark)** — the passive scanner is now extensible: drop a `.star` file
  defining `def check(flow): …` into `~/.interceptor/checks/` and it runs on every scan beside the
  built-ins. New `internal/checkscript` compiles and runs checks in an embedded **Starlark** engine
  that is **sandboxed** (no file/network/clock access, no `load()`/imports — safe to share) and
  **step-bounded** (a runaway check aborts, never hangs a scan); broken/erroring checks are logged
  and skipped. The `flow` object exposes method/scheme/host/port/path/status/mime, bodies, headers
  (dicts + case-insensitive `req_header`/`res_header`), and `query_param`; builtins `finding(…)` and
  `re_search(…)`. Documented as the authoring **standard** in [docs/custom-checks.md](docs/custom-checks.md)
  with ready-to-copy [`examples/checks/`](examples/checks/) (guarded by a test that they compile). TDD.

### Removed
- **CI / release workflows pulled from version control (temporary).** Pushing `.github/workflows/*`
  requires a git token with the `workflow` scope; to publish the rest of the work without it, the CI
  and release workflows were removed from the tree and `.github/` is now gitignored (the files stay
  on disk). Re-enable the per-tag binary releases described under 0.1.1 with
  `git add -f .github/workflows && git commit && git push` once that scope is granted. (Until then the
  README's CI badge shows no status.)

### Fixed
- **`SQLITE_BUSY` ("database is locked") under write contention.** `busy_timeout` and `synchronous`
  are *per-connection* pragmas, but they were set once via `db.Exec` — which configures only one
  connection in `database/sql`'s pool; the others had a 0 ms busy timeout and failed *immediately*
  under concurrent writes (proxy capture, active-scan probes, settings), occasionally dropping a
  write. They're now applied to **every** connection via the DSN (`_pragma=busy_timeout(10000)`, WAL,
  `synchronous(NORMAL)`, `foreign_keys(1)`), so contending writers wait their turn instead of
  erroring. Guarded by a concurrency stress test (16 writers × 40 inserts + concurrent readers).

## [0.1.1] — 2026-06-23

### Added
- **Release automation** — a `release` GitHub Actions workflow cross-compiles static binaries
  (linux / macOS / windows × amd64/arm64) and attaches them with a `checksums.txt` to each `v*`
  tag's GitHub Release, so users can download-and-run without a Go toolchain.
- **CI workflow** — `go vet` + `-race` tests + a static build run on every push to `main` and on PRs
  (with a status badge in the README).
- **`SECURITY.md`** — a private vulnerability-reporting / responsible-disclosure policy.

## [0.1.0] — 2026-06-23 · first public release

The first tagged release: an intercepting HTTP/HTTPS proxy and AI-operable security-testing toolkit
in a single static Go binary — TLS MITM with on-demand leaf certs, request/response interception +
match-&-replace, Repeater / Intruder / Scanner, target scope, HAR + portable projects, WebSocket
capture & replay, BYO-key AI assist (Anthropic / OpenRouter), an MCP server (stdio + Streamable-HTTP,
24 tools), and a loopback-only control plane hardened against CSRF / DNS-rebinding.

### Security
- **Control-plane CSRF / DNS-rebinding guard** — the control API and UI on `:9966` now reject any
  request whose `Host` is not a loopback name, and any request carrying a non-loopback `Origin`.
  Both listeners already bound `127.0.0.1`, but a web page the user visited could still POST to
  `http://127.0.0.1:9966` (CSRF) or, via DNS rebinding, read responses — and through `PUT /api/settings`
  rebind the proxy to `0.0.0.0`. The guard (`securityGuard` in `internal/control/guard.go`) closes
  this: the Host check defeats rebinding, the Origin check defeats classic CSRF; legitimate clients
  (the embedded UI, curl, the MCP server) pass untouched. **Rebinding the proxy to a non-loopback
  address is now refused** unless `INTERCEPTOR_ALLOW_EXTERNAL_BIND=1` is set (the deliberate
  "expose to my LAN" path). TDD + verified live (normal use 200; cross-origin/rebind 403/400).

### Added
- **`LICENSE`** — the project is MIT licensed.
- **Public-ready README** — install via `go install …@latest` / `@v0.1.0`, a quick-start, an env-var
  config table, a prominent responsible-use notice, the full feature list, an updated architecture
  table, and badges.
- **WebSocket message replay** (a WS Repeater) — a new `internal/wsrepeater` opens a fresh
  WebSocket to a target, sends one message, and captures the reply frames, speaking enough of
  RFC 6455 to do so with no external deps (client handshake with `Sec-WebSocket-Accept`
  validation, masked client frames, frame reading; TLS verification skipped for `wss`). Drive it
  from the WS frame inspector's **Replay a frame** box, `POST /api/ws/send`, or the MCP **`ws_send`**
  tool (now **24 MCP tools**) — so the AI can fuzz a socket too. Optional binary frames and extra
  handshake headers (e.g. a Cookie). TDD incl. the RFC accept-key vector and a full echo round-trip.
- **Findings → Markdown report** — a new `internal/report` renders the passive-scan findings as a
  severity-grouped Markdown report (summary line, per-finding target/detail/evidence/remediation).
  Download it from the Scanner tab's **Export report** button (`GET /api/scanner/report`) or pull it
  via the MCP **`scan_report`** tool (now **23 MCP tools**) to drop straight into a writeup. Pure,
  deterministic, TDD'd (incl. inline-code sanitization of evidence).
- **Four more passive scanner checks** (8 → 12) — reflected request parameter in an HTML response
  (possible XSS sink, with a noise guard for trivial/short values), HTTP Basic authentication (High
  over plaintext), missing `X-Content-Type-Options: nosniff` on scriptable responses, and missing
  clickjacking protection (no `X-Frame-Options` / CSP `frame-ancestors`). They flow through to the
  Scanner tab **and** the AI's `analyze_flow` / summarize. TDD, including a no-false-positive guard.
- **Flow → curl** — a new `internal/curlgen` renders a captured request as a runnable `curl`
  command (direct to target: `--path-as-is` to preserve the exact path, `-k` to skip TLS
  verification — matching how Interceptor talks to targets). Exposed at `GET /api/flows/{id}/curl`
  and as an MCP **`flow_as_curl`** tool (now **22 MCP tools**) so the AI can hand the user a repro
  command. Complements the UI's existing *proxy-routed* "Copy as cURL" (which replays through
  Interceptor); this one is standalone. TDD on the renderer (escaping, header order, body).
- **Session / auth header injection** — a set of headers (typically an `Authorization` bearer token
  or a `Cookie`) is now auto-applied to every **Repeater** and **Intruder** send, which bypass the
  proxy and so were previously unreachable by match-&-replace rules. Keeps sends authenticated
  without re-pasting a token; the injected headers are recorded on the resulting flow. Applied in the
  shared `sender` (so both modules and the AI's `send_request` benefit), configured via
  `GET/POST /api/session`, persisted in settings and loaded at startup, with a **Settings → Session**
  toggle + editor and an MCP **`set_session`** tool (now **21 MCP tools**) so an agent can keep its
  own traffic authenticated. Replace-semantics (a session value overrides a stale one). TDD on the
  injector; verified live end-to-end. (Login-macro recording and automatic re-auth on 401 remain
  roadmapped.)
- **MCP Streamable-HTTP transport** — besides the `interceptor mcp` stdio subcommand, the control
  port now serves the MCP "Streamable HTTP" transport at **`POST /mcp`**: a remote/hosted agent can
  drive the same 20 tools over HTTP JSON-RPC without spawning a subprocess. Stateless (no session
  id); single messages and JSON-RPC batches; notifications return `202`; `GET /mcp` returns `405`
  (no server-initiated stream). Implemented as `mcp.Server.ServeHTTP`, mounted in `control` and
  pointed back at the control server (the same wiring stdio uses). `/api/mcp` advertises
  `httpTransport`; the MCP tab shows the endpoint. Transport unit-tested + live end-to-end verified.
- **AI assist (bring-your-own-key, multi-provider)** — `internal/aiassist` calls **Anthropic**
  (native Messages API) or **OpenRouter** (OpenAI-compatible chat completions, fronting many models)
  with a user-supplied key to **explain** a request, **suggest** payloads, or **summarize** findings.
  Off until a key is set (Settings → AI assist → choose a **Provider**, or `ANTHROPIC_API_KEY` /
  `OPENROUTER_API_KEY`); the exchange is sent to the chosen provider only on an explicit ✨ click.
  Control: `POST /api/ai/assist`; settings store provider + key (never returned) + model; the
  inspector gets a ✨ button + result modal. Both providers' request construction and error handling
  are unit-tested; declines cleanly (400) without a key.
- **`analyze_flow` (AI tool)** — `GET /api/flows/{id}/analyze` and a matching MCP tool return a
  compact, decision-ready summary of a flow: URL/status, notable security headers, query parameters
  (injection points), passive scanner findings, and in-scope status — so an agent can triage without
  re-fetching and parsing the raw exchange (now **20 MCP tools**).
- **Benchmark guard** — `BenchmarkInsertFlow` (metadata write rate) joins `BenchmarkTeeBody`, and
  `scripts/bench.sh` reproduces the documented numbers (Go benchmarks + cold start + idle RSS).
- **Saved filters / views** — name and recall a history filter (scheme/method/status/search/host +
  the in-scope toggle). Store `saved_views` + `GET/POST/DELETE /api/views` (SSE `views.update`); a
  toolbar **views** dropdown with save (＋) and delete (✕). TDD.
- **Projects (save/load)** — export/import a portable session: captured flows (as HAR) + match-&-
  replace rules + target-scope rules + the upstream-proxy setting, in one JSON bundle. Control:
  `GET /api/export/project`, `POST /api/import/project` (additive merge; does not rebind the
  listener); **Settings → Project** Export/Import buttons. Round-trip tested.
- **Benchmarks** — `docs/benchmarks.md` with measured numbers (~20 MB idle RSS, ~1 s cold start,
  ~444 MB/s capture at ~1.5 KB/op) plus a `BenchmarkTeeBody` that proves capture streams rather than
  buffers. The product **roadmap** rolled to cycle 2 (cycle 1's Now+Next slate shipped); PRD-0001
  marked Shipped.
- **Upstream / chained proxy** — route Interceptor's outbound traffic through another proxy (e.g. a
  corporate proxy). Configured via the `upstream.proxy` setting (`PUT /api/settings`
  `{upstreamProxy}`) and a **Settings → Upstream proxy** field; applied race-safely to the transport
  (`atomic.Pointer`), live and at startup. Blank = direct.
- **Response interception** — the intercept engine now handles the response side too:
  **response match-&-replace** (`res-header` / `res-body` rules execute, transforming responses
  before they reach the client) and a **response hold queue** (hold → edit raw → forward / drop),
  mirroring the request side. Wired into both the HTTP and MITM response paths (buffered only when a
  response rule or response-intercept is active; otherwise still streamed). Control adds
  `POST /api/intercept/response/toggle|{id}/forward|{id}/drop` and the intercept state carries
  `responseEnabled` + `responseQueue`; the Intercept tab gains a response toggle, a response hold
  queue, and `res-*` rule types; MCP gains `set_response_intercept`. Verified live (a `res-body` rule
  rewrote a real HTTPS response).
- **System-proxy toggle** — `internal/sysproxy` points the OS HTTP/HTTPS proxy at Interceptor on
  macOS (via `networksetup`) and back off, only ever on explicit user opt-in (never automatic).
  Control: `GET/POST /api/sysproxy`; a **Settings → System proxy** toggle; other platforms get a
  "set 127.0.0.1:8080 manually" hint. Removes the top setup friction on macOS.
- **HAR export & import** — `internal/harx` converts flows to/from HAR 1.2 (round-trip tested).
  Control: `GET /api/export/har` (optionally `?inScope=1`; excludes Intruder noise) streams the
  history as a downloadable `.har`; `POST /api/import/har` ingests a HAR, recording each entry as a
  flow tagged `FlagImported` (bodies stored, viewable/replayable). The Proxy toolbar gains **Export**
  and **Import** — interop with browsers, Postman, and other tools (free; some competitors gate this).
- **Target scope** (PRD-0001) — `internal/scope`: ordered include/exclude rules over host
  (`*.acme.com` wildcard), path prefix, scheme, and port; "any include matches and no exclude
  matches" semantics (exclude wins; no rules = everything in scope). Scope focuses the tool without
  affecting capture: a **◎ in scope** toggle filters the history (`GET /api/flows?inScope=1`), the
  **intercept gate** only holds in-scope requests, and the **scanner** only analyzes in-scope flows.
  Control: `GET/POST/PUT/DELETE /api/scope` + SSE `scope.update`; **Settings → Target scope** rule
  editor; MCP tools `list_scope` / `add_scope_rule` (now 18 tools). Verified live.

### Changed
- **History search** now matches **method, host, or path** (was path-only) — the toolbar search box
  broadened accordingly. (`QueryFlowsFilter.Search`, tested.)
- **Onboarding** — the empty Proxy history is now a "get started" card: the proxy address to point a
  client at, a one-click CA download for HTTPS, a right-click hint, and a **Connect via MCP** button
  that jumps to the setup — reducing the biggest first-run friction for the human half of the pair.

### Added
- **Real MCP server** (`interceptor mcp`) — a stdio JSON-RPC 2.0 Model Context Protocol server
  (new `internal/mcp`) that lets an AI assistant operate Interceptor with the same capabilities as
  the UI. It drives a running instance over the control API and exposes **16 tools** (`list_flows`,
  `get_flow`, `send_request`, `start_intruder`, `intruder_state`, `run_scanner`, `list_issues`,
  `get_intercept`, `set_intercept`, `forward_request`, `drop_request`, `list_rules`, `add_rule`,
  `list_ws_frames`, `get_settings`, `ca_info`) with **bounded results** so large bodies don't blow
  the agent's context. The `/api/mcp` descriptor now advertises the real server + a ready-to-paste
  client config; the **API → MCP** UI tab shows that config (one-click copy), the connect command,
  and the live tool list. README gained a "Drive it with AI (MCP)" section. (`INTERCEPTOR_CONTROL_URL`
  overrides the control target.)
- **Product-management docs** under `docs/product/`: a market-researched product **strategy**
  (vision, personas, competitive positioning vs Burp/ZAP/Caido/mitmproxy/Hetty, non-goals), a
  prioritized **roadmap** (Now/Next/Later with RICE scoring), **success metrics** (North Star =
  Weekly Active Hunters, funnel KPIs, privacy-first measurement), and a flagship **PRD** for Target
  Scope (also the PRD template). Linked from the README.
- `README.md` (product overview, install/run, HTTPS CA setup, architecture, control API) and
  `CONTRIBUTING.md` (the code standards every change must follow — TDD, no cgo, conventional
  commits, changelog-per-change, package/concurrency/UI conventions).

### Changed
- Rewrote `CLAUDE.md` to document the Go application (it previously described the now-removed
  design component) and to point at `README.md` / `CONTRIBUTING.md`.
- Tidied `.gitignore` (dropped the obsolete source-archive entry; ignore in-tree runtime data).

### Removed
- The obsolete mock-UI design artifacts, superseded by the real Go app + embedded UI:
  `Conduit.dc.html`, `support.js`, `screenshots/`, `.thumbnail`, and the source `.zip`.
  (Recoverable from git history if ever needed.)

## [2026-06-22] — Product modules: Repeater, Intruder, Scanner, WebSocket capture, API

### Added
- **API module** — `internal/store` API-key management (token minted once, only its SHA-256 hash + a short prefix stored; create/list/revoke/verify). Control: `GET/POST/DELETE /api/keys`, `GET /api/reference` (machine-readable list of all 25 control routes), `GET /api/mcp` (a preview MCP descriptor mapping intended tools — list_flows, send_request, run_scanner, … — onto the REST API; a full MCP server is deferred with a note). New **API** UI tab with Keys / REST / MCP sub-tabs. Verified live.
- **WebSocket frame capture** — the WebSocket tunnel is now frame-aware: it parses RFC 6455 frames in both directions (handling client masking), records each frame's direction, opcode, length, and a bounded unmasked payload preview to a `ws_frames` table, and still forwards bytes verbatim (large frames streamed, never buffered whole). Control: `GET /api/flows/{id}/ws`, SSE `ws.frame`. The Proxy inspector shows a live frame list (send/recv arrows, opcode labels, preview) when a WebSocket flow is selected. Verified live through a real browser against `wss://ws.postman-echo.com` (text + ping/pong/close frames captured).
- **Scanner module** — `internal/scanner` runs passive security checks over already-captured flows (no traffic sent, off the hot path): password in request body, session token/JWT in response body, verbose 5xx error disclosure, missing CSP on HTML, missing HSTS on HTTPS, wildcard CORS, cookies without Secure+HttpOnly, and server version disclosure. Findings (`store.Issue`, severity High/Medium/Low) are deduped by (title, target) and persisted in a `scan_issues` table. Control: `POST /api/scanner/run`, `GET /api/scanner/issues`, SSE `scanner.update`. New **Scanner** UI tab — issues sorted by severity with a detail pane (description, evidence, remediation). Verified live against real sites.
- **Intruder module** — `internal/intruder` runs payload-driven attacks against a request template whose fuzz points are wrapped in `§…§` markers: **Sniper** (vary one position at a time) and **Pitchfork** (walk lists in parallel). Each generated request is sent via `internal/sender` (recorded as a `FlagIntruder` flow), results carry status/length/time, and anomalies (status ≠ the modal status, or ≥500) are flagged. Runs one attack at a time in the background, capped at 2000 requests. Control: `POST /api/intruder/start`, `GET /api/intruder/state`, SSE `intruder.update`. New **Intruder** UI tab (template with a § wrap helper, payload lists, attack-type toggle, live results table) plus a **Send to Intruder** right-click action. Fuzz points in the request line/path/headers/body all apply (each substituted request is re-parsed). Verified live.
- **Repeater module** — `internal/sender` sends one-off requests directly to a target (bypassing the proxy listener; does not follow redirects; does not verify TLS, since a pentest tool talks to targets with bad certs) and records each as a flow tagged `FlagRepeater`. New control endpoints `POST /api/repeater/send` and `GET /api/repeater/history`; the Proxy history now excludes Repeater/Intruder sends (`QueryFlowsFilter` gained `RequireFlags`/`ExcludeFlags`). New **Repeater** UI tab (method/URL/headers/body editors, Send, response raw/pretty, send history) plus a **Send to Repeater** right-click action that prefills the editor from a captured flow. Verified live against real sites.

### Fixed
- **Intruder anomaly flagging no longer mis-flags valid responses.** Parse/transport failures (recorded with `Status 0`) were counted toward the modal status, so an attack with several malformed payloads could make `0` the mode and flag every genuine `200` as an anomaly. The mode is now computed only over successfully-sent responses, and unsent rows are never flagged. (Found in post-build code review.)
- **CONNECT tunnels left idle between requests are now reaped** via a 3-minute read deadline applied only while awaiting the next request — cleared during request processing so slow bodies and long-lived (legitimately idle) WebSocket splices are unaffected. Upstream dials enable TCP keep-alive so a half-open peer is detected without an application timeout that would kill an idle-but-alive socket. (Found in post-build code review.)
- **WebSocket connections through the proxy no longer break.** Upgrade handshakes were sent down the normal forward path, which strips the `Connection`/`Upgrade` hop-by-hop headers and uses `http.Transport.RoundTrip` (no protocol upgrade) — so the origin received a plain GET and returned `500 "WebSocket upgrade is expected"`. The proxy now detects `Connection: Upgrade` requests (HTTP and MITM'd HTTPS), forwards the handshake verbatim, relays the `101`, and splices bytes bidirectionally — keeping `ws://`/`wss://` connections working. The handshake is recorded as a flow (new `FlagWebSocket`); frame-level capture remains a later slice. Intercept/match-&-replace are bypassed for upgrades.

### Added
- **Right-click context menu on history rows** — cell-aware quick filters ("Filter host / method / status / scheme", with the clicked column's filter listed first and highlighted) plus "Copy URL" and "Copy as cURL" (reconstructs a runnable `curl -x <proxy>` command with headers and body). Active filters now show as removable chips below the toolbar, kept in sync with the toolbar controls.

### Changed
- **UI dark-mode contrast** raised to meet WCAG AA: brightened the dim text tokens (`--fg2`, `--fg3`), lifted surface/line tokens off pure black, and enlarged the smallest table text (header 9→10px, rows 11→11.5px) for legibility.

## [2026-06-22] — Slice #1: core intercepting proxy (Go core + web UI)

### Added
- Design spec for slice #1 (core intercepting proxy): `docs/superpowers/specs/2026-06-22-interceptor-proxy-core-design.md`. Stack: Go core (single static binary) + web UI; persistent-lean storage (SQLite metadata + on-disk bodies); proxy listener configurable at runtime (default `127.0.0.1:8080`); control plane on `127.0.0.1:9966`.
- Implementation plans (TDD, bottom-up): the foundation slice (`docs/superpowers/plans/2026-06-22-interceptor-foundation.md`) and the completion slice — TLS MITM, intercept, control, UI (`docs/superpowers/plans/2026-06-22-interceptor-slice1-completion.md`).
- **Foundation (Go):** `internal/store` (SQLite flow metadata + settings, content-addressed deduplicated on-disk body store), `internal/capture` (streams bodies to disk via `io.TeeReader`, never buffering whole bodies), `internal/proxy` (HTTP forward proxy capturing every flow, hop-by-hop header stripping, errored-flow recording on upstream failure). Pure-Go SQLite (no cgo) → single static binary.
- **TLS interception** — `internal/tlsca` (local CA generate/load under `~/.interceptor/ca/`, on-demand cached per-host leaf minting) plus `CONNECT` handling in `internal/proxy` that terminates client TLS with a minted leaf and captures HTTPS flows. A shared gate/forward/capture core serves both the HTTP and HTTPS paths.
- **Request intercept + match-&-replace** — `internal/intercept`: a Burp-style hold queue (forward [optionally edited] / drop) that blocks the proxy goroutine while a request is held, plus an ordered request-side regex match-&-replace engine (header/body). Wired into the proxy request path; flows record intercepted/edited/dropped flags.
- **Control plane** — `internal/control`: a localhost REST API (flows list/detail/raw, rules CRUD, intercept toggle/forward/drop + queue, settings, CA download) and a Server-Sent-Events stream broadcasting `flow.new` / `intercept.update`. Serves the UI.
- **Web UI** — `internal/control/ui/index.html` (embedded via `go:embed`): dark theme matching the Conduit design tokens; live HTTP/HTTPS history table, request/response inspector (raw/pretty), Intercept tab (toggle, hold-queue forward/drop with editable raw, match-&-replace rules), and Settings (proxy listener rebind, CA download). Brand favicon embedded inline (no extra request).
- **Runnable binary** — `cmd/interceptor` now runs two listeners: the proxy (default `127.0.0.1:8080`, overridable via the `proxy.addr` setting) and the control plane on `127.0.0.1:9966`. Supports **runtime proxy rebind** (opens the new listener first; a failed rebind keeps the old one), restores the persisted intercept toggle, opens the UI in the default browser (suppress with `INTERCEPTOR_NO_BROWSER`), and shuts both down gracefully. Verified end-to-end: UI reachable, and live capture of proxied **HTTP and HTTPS** traffic.

### Changed
- Product renamed from "Conduit" to **Interceptor**.
- `proxy.New` now takes a CA, an intercept engine, and an events sink (all optional/nil-safe); `CONNECT` is handled rather than returning 501.
- `internal/store` gained match-&-replace `rules` CRUD, flow `flags`, and `QueryFlowsFilter` (method / host / path-search / status-class / scheme + cursor pagination, pushed down to SQL).

## [2026-06-22] — Project setup

### Added
- Imported the Conduit design specification (intercepting HTTP proxy / HTTP client UI) from the source archive: `Conduit.dc.html`, the `support.js` runtime, and `screenshots/`.
- `CLAUDE.md` documenting the Design Component architecture, the `renderVals()` render-derived-view-model pattern, the `<sc-for>` / `<sc-if>` / `{{ }}` template DSL, and the six product modules.
- `CHANGELOG.md` (this file) plus a changelog-update policy in `CLAUDE.md`.
- `Stop` hook (`.claude/hooks/changelog-reminder.sh`, wired via `.claude/settings.json`) that reminds Claude to update this changelog when project files change without a matching entry.
- Initialized the git repository (`main`) and added `.gitignore`.
