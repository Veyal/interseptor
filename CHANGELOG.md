# Changelog

All notable changes to the Conduit design project are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
each "release" is an iteration of the Conduit design (`Conduit.dc.html`).

## [Unreleased]

### Added
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
