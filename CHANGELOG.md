# Changelog

All notable changes to **Interceptor** are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).


> **Archive:** Release notes for 0.11.0 and earlier live in [CHANGELOG/archive/pre-0.12.md](CHANGELOG/archive/pre-0.12.md).

## [Unreleased]

### Added
- **Choose a custom save location when creating a project.** Settings → Project & data and the top-bar Projects picker both gain an optional "save location" field — leave it blank to keep the default `~/.interceptor/projects/<name>/`, or point it at any absolute folder (e.g. a client-engagement drive) to store that project's history, rules, and scope there instead. The switch endpoint's existing bare-name-only restriction (a deliberate defense against a loopback request relocating the process to an arbitrary path) is unchanged — a custom path is a separate, explicit opt-in field, rejected if it isn't absolute or resolves to a filesystem/drive root. Chosen folders are remembered (`~/.interceptor/external-projects.json`) so they reappear in the switcher without retyping the path.

### Changed
- **Custom checks editor revamped.** The modal is much larger (was fixed at 980×820, now scales up to 1400×920) and the sidebar gained a filter box that searches by title/id and auto-expands matching built-in/active groups. Test and Save results now render as color-coded status cards (red for errors with a monospace stack trace, amber for a finding, green for success/no-finding) instead of being crammed into a fixed 140px text box — the output area now grows with content up to a sane cap. The single Revert/Delete button's label and enabled state now update live (grayed-out "↺ Revert" when a built-in has no override to revert, "🗑 Delete" for anything else) instead of a static label that didn't say which action it would take.

### Fixed
- **Switching (or creating) a project could reload the UI into stale data from the project you just left.** The reload-readiness check polled `/api/version`, which any live server answers identically — old or new — since the version string doesn't encode which project is loaded. On Windows, the outgoing process keeps the control port bound for up to ~800ms after answering the switch request while the incoming one is still binding, so the client's first poll (at ~500ms) very often hit the *old* process and reloaded before the handover finished, leaving the old project's proxy history and other data on screen until a second, manual refresh. The check now polls `/api/project` and waits for `current` to actually flip to the new project before reloading (bounded by a ~5s grace period so re-selecting the *same* project doesn't hang).

## [0.25.0] - 2026-07-03

### Security
- **Removed engagement artifacts committed to the repo** (a one-off bounty script and a pentest spec) that contained real target hosts, vulnerability details, and third-party PII, and **purged them from git history**. Redacted test fixtures that echoed real captured data (CONNECT hosts/IPs, a session-cookie name) to RFC-5737 / RFC-2606 placeholders, and added `.gitignore` guards so engagement artifacts cannot re-enter.

### Changed
- **Quiet by default:** dropped the per-response `proxy: MITM resp …` debug log that fired on every intercepted HTTPS response (a leftover from the HTTP/2-downgrade work) and spammed the console while a device drove traffic. Real capture failures are still logged.
- **Critical findings now render a filled-red severity badge.** `.sev.Critical` had no styling, so Critical scanner/check results showed a text-colored outline instead of red; it's now a solid red badge, visually distinct from High.
- **Content discovery no longer re-sorts its whole result set on every hit.** `appendResult` used to run a stable sort under the engine lock for each finding — O(n² log n) over a run, and it blocked concurrent state polls. Recording is now O(1); the depth-then-path ordering is applied once to the snapshot when the UI reads `State()`. A forced-browse run that finds thousands of paths no longer degrades or stalls the live UI.
- **SSE broadcast no longer holds the hub lock across the client fan-out.** It snapshots the subscriber channels under a short lock and sends outside it, so heavy capture no longer contends with client connect/disconnect. Sends stay non-blocking.

### Fixed
- **WebSocket repeater caps total retained payload at 32 MB** across all collected reply frames. Each frame was already capped at 16 MB, but up to 64 frames could otherwise buffer ~1 GB for a single repeat.
- **Intercept no longer forwards a truncated body** when an operator edits a request whose body exceeds the 64 MB editor buffer. Such requests now bypass the hold and forward their full original stream unedited (a >64 MB body can't be meaningfully hand-edited), instead of reconstructing a truncated body from the editor dump.
- **Map cluster badges** (`+N identical` / soft-404 clusters) drew their border from an **undefined `--border`** token, so it fell back to the text color; now uses `--line2`.
- **Map "searching bodies" warning strip** had **no background** — it referenced an undefined `--amberDim` with no fallback; now uses the existing amber-tinted `--noteDim`.
- **Response capture no longer records a truncated body as authoritative** when the client aborts mid-download. The plain-HTTP path previously overwrote the flow's body hash/length with the partial finalize result and left the flow unflagged; it now marks such flows with `FlagCaptureError` so history and replay don't treat an incomplete body as the full response.
- **Windows setup CA step** now notes that "Local Machine" trust needs admin and points non-admins to "Current User".

### Removed
- **Dead UI code:** `hideInspectorDecodeBars` (proxy.js) and `chainLayout` (findings.js) — neither was referenced anywhere.

## [0.24.0] - 2026-07-03

### Added
- **AI provider: GLM Coding Plan (Zhipu / z.ai).** A third AI-assist provider alongside Anthropic and OpenRouter. GLM's coding plan exposes an Anthropic-compatible Messages endpoint (`https://api.z.ai/api/anthropic`), so it reuses the native request/response path with a Bearer token. Settings → AI assist shows a **model picker with the full GLM lineup** — the flagship `glm-5.2` (1M context), `glm-5.1`, `glm-5-turbo`, `glm-5`, `glm-4.7`/`glm-4.6`/`glm-4.5` and the free flash tiers — defaulting to `glm-5-turbo` (the GLM-5 speed model). Key also read from `GLM_API_KEY`/`ZAI_API_KEY` env. Agent mode (let-AI-send-requests) works since GLM speaks the Anthropic tool-use format.
- **Update-check cache.** `internal/version/github_cache.go` caches the last "latest version" lookup for an hour at `~/.interceptor/update-check.json`, avoiding redundant GitHub calls.
- **Rate-limit fallback for `interceptor update`.** When the GitHub API is rate-limited (or no `GITHUB_TOKEN` is set), version checks and release fetches fall back to the unauthenticated `releases/latest` redirect and synthesize release asset URLs from GoReleaser naming, verifying each with a HEAD/range request before use.

### Changed
- **GitHub API errors** now surface a `Retry-After`/`X-RateLimit-Reset` hint when available.
- **Command palette (Ctrl+K)** now finds more of the app: switch/create project, toggle theme, and send the selected flow to Repeater/Intruder, in addition to existing tab/settings navigation.
- **Proxy History gives the flow list the whole screen until you need the inspector.** The request/response inspector (plus its splitter and note bar) is only useful once a flow is picked — until then it was ~40% of the viewport showing two "select a flow" placeholders while the flow list, the thing you actually scan, was squeezed to ~36%. It's now hidden when nothing is selected (flow list ≈79% of the viewport, roughly 2× the visible rows) and reappears at its remembered height the instant a row is clicked — the same detail-on-demand pattern as Chrome DevTools' Network panel and Burp's history.
- **Top-bar declutter.** The header device-proxy chip is hidden when it resolves to a loopback address, which merely duplicated the listener address beside it (`127.0.0.1:8080  127.0.0.1:8080`). It reappears the moment a real, phone-reachable LAN endpoint exists — which is the only time it carries information.
- **Settings — real wide-desktop layout.** Every section was hard-capped at 780px and pinned left, so on a wide window ~40% of the screen was dead space and the page was one tall single-column scroll. Multi-card panels (Proxy & network, Project & data, TLS/CA) now lay out as a **responsive 2–3 column card grid** (like macOS System Settings) that collapses to one column on narrow windows; a section carrying a data table (Data & retention) spans the full width. Single-card panels and the Session panel (whose extras are collapsible `<details>`) stay a normal-width vertical flow, so a lone form is never dragged edge-to-edge. Implemented with `:has()` — where unsupported it degrades to the prior block layout.
- **Settings — nav reordered by how often each panel is used** during an engagement: Proxy & network → **Project & data** (frequent project switching) → Target scope → Session / auth → TLS / CA → Scanner → AI assist → API & MCP. Previously Project & data sat second-to-last despite being one of the most-used panels.

### Fixed
- **Settings — "Control UI" Host/Port fields no longer float disconnected in the middle.** The row used `class="field row"`, but `.field`'s `flex-direction:column` overrode `.row`, so Host and Port stacked and right-aligned instead of sitting side by side. Rebuilt as a proper `.row` of `.field`s (Host grows, Port fixed-width), left-aligned like the rest of the form.
- **Settings — "Proxy listener" Host dropdown now fills its cell** instead of shrinking to content width and leaving a large gap before the Port field. The enhanced `.ui-select-btn` trigger defaults to `width:auto`; listener host selects are now forced to `width:100%`.
- **Proxy History — "Views ▾" menu.** Clicking it opened and instantly closed the dropdown on the same click, because the app-wide "click outside closes the context menu" listener fired for the very click that opened it. It's the only context menu wired to a left-click handler instead of right-click, so it needed its own `stopPropagation()` (matching the pattern already used elsewhere).
- **Custom `<select>` dropdowns showing a stale label.** `enhanceSelect`'s trigger button only re-rendered on a native `change` event, but code across the app sets `select.value = x` programmatically when loading data (e.g. Settings → AI assist loading the saved provider) — which never fires `change`. The dropdown's underlying value was correct but the visible label was wrong (e.g. showed "Anthropic" while "OpenRouter" was actually selected/saved). The `value` property on enhanced selects now re-syncs the visible widget on every set, closing the whole bug class instead of one instance.
- **Toolbar buttons/toggles with longer labels wrapped across 2–3 lines** (e.g. Map's "Hiding 403/404-only", "Collapsing identical"), making toolbars taller and harder to scan. `.btn`/`.toggle` now stay single-line (`white-space:nowrap`) and rely on the toolbar's existing horizontal scroll for overflow, consistent with the rest of the app.
- **Intruder "Start" silently no-oped** when the target field was empty or no `§` markers were set — a toast fired but auto-dismissed with no lasting indication of what to fix. Both validation failures now focus the offending field.

## [0.23.0] - 2026-07-02

### Added
- **Invisible (transparent) proxy mode.** A new on/off toggle (Settings → Invisible Proxy, `proxy.invisibleProxy`) lets Interceptor accept traffic from clients that aren't proxy-configured — e.g. redirected via `iptables`/`pf`, DNS spoofing, or port forwarding — matching Burp's "Support invisible proxying". When enabled, origin-form requests (`GET /path` + `Host:` header) are forwarded to the host named in their `Host` header instead of being rejected as malformed proxy requests. Absolute-URI and `CONNECT` requests keep working unchanged. Off by default.

## [0.22.0] - 2026-07-01

### Added
- **Jailbroken iOS over SSH.** Automate proxy and CA installation on device via SSH (control API, MCP, Settings). New `internal/ios/ssh` client and `ios_ssh` control handlers.
- **Device proxy endpoints.** LAN device endpoint selection and multi-listener proxy addresses (`device_endpoint`, `proxyaddrs`) so clients can target the right listen address on multi-homed hosts.
- **Network helpers.** `internal/netutil/hosts` for resolving/advertising host addresses used by endpoint selection.

### Changed
- **TLS diagnosis and listener settings.** Streamlined TLS diag UI and proxy listener configuration in Settings.
- **MCP** — expanded iOS/device-proxy tooling alongside existing setup flows.

### Fixed
- **Map UI** — subdomain select dropdown scrolling.
- **Intruder** — payload panel layout.
## [0.21.1] - 2026-07-01

### Fixed
- **Windows interceptor update.** The helper batch script now waits for the CLI to exit, stops other interceptor.exe processes, retries the binary replace (up to 90 attempts), auto-restarts Interceptor, and writes failures to interceptor-update.log beside the binary.
## [0.21.0] - 2026-07-01

### Changed
- **External bind allowed by default.** Rebinding the proxy or control UI to `0.0.0.0` (or any non-loopback address) no longer requires `INTERCEPTOR_ALLOW_EXTERNAL_BIND=1`. Default listen addresses stay loopback; set `127.0.0.1` in Settings to stay localhost-only. Set `INTERCEPTOR_ALLOW_EXTERNAL_BIND=0` to lock down non-loopback rebinding.

### Added
- **SSL pinning / TLS MITM failure detector.** When a mobile app sends CONNECT but rejects the proxy's leaf certificate, Interceptor now records a `FlagTLSFailed` flow (tagged `tls-failed`, `ssl-pinning?`) instead of silently dropping the tunnel. New `GET /api/tls-diagnosis` and MCP `detect_ssl_pinning` distinguish **tls_blocked** (pinning or untrusted CA) from **no_traffic** (proxy bypass) and **no_https** (cleartext only). `check_readiness` adds a `tls_intercept` blocker; History shows a red **PIN** badge on failed handshakes. UI: live banner in Proxy History + Settings â†’ TLS â†’ SSL pinning section (explicitly states Interceptor cannot bypass pinning â€” device-side Frida/APK patch required).
- **iOS automation (Settings â†’ TLS â†’ iOS).** Simulator: install CA via `simctl keychain add-root-cert` and open a `.mobileconfig` (CA + global HTTP proxy) in Safari. Physical iPhone: download/serve profile at `GET /api/ios/profile.mobileconfig` â€” install in Safari, enable full trust. MCP: `ios_status`, `ios_setup`, `ios_install_ca`. Optional `libimobiledevice` for USB device listing. Does not bypass SSL pinning (same as Android).

### Fixed
- **`interceptor update` GitHub 403** â€” update check sends a proper `User-Agent`, falls back to the public `/releases/latest` redirect when the API is rate-limited, and accepts `GITHUB_TOKEN` for authenticated quota.

## [0.20.0] - 2026-06-30

### Added
- **CI workflow** (`.github/workflows/ci.yml`) â€” `go vet`, `go test`, `go test -race`, and cross-platform `CGO_ENABLED=0` builds on every push/PR.
- **Release workflow** (`.github/workflows/release.yml`) + **GoReleaser** (`.goreleaser.yaml`) â€” multi-arch binaries and `checksums.txt` on `v*` tags.
- **`internal/hostpattern`** â€” shared `*.wildcard` / exact host matching for retention purge and scope rules.
- **`internal/strutil.AtoiOr`** â€” single implementation replaces copy-pasted `atoiOr` in proxy/sender/control.
- **`internal/httplines.ParseRawRequest`** â€” shared raw HTTP request parsing for Repeater, Intruder, and login macro.
- **`breaker` package tests** â€” concurrent circuit-breaker coverage for the active-scan engine.
- **`apiTry()` helper** â€” optional toast-on-error wrapper for fire-and-forget UI `api()` calls.

### Changed
- **`interceptor update` progress.** Step-by-step status (check â†’ download â†’ verify â†’ extract â†’ install) plus live download percentage and size when the terminal supports it.
- **Control API layout** â€” domain-scoped route registration (`routes_register.go`, `api_types.go`); HTTP handlers grouped by `flowAPI`, `interceptAPI`, `settingsAPI`, etc.
- **Scope host matching** â€” wildcard/exact host rules delegate to `hostpattern.MatchHost`.
- **CHANGELOG** â€” pre-0.12.0 release notes archived to `CHANGELOG/archive/pre-0.12.md`.
- **`apiTry()` rollout** â€” Map endpoints loader and Scanner active-scan/OOB polls use the helper instead of silent `catch`.
- **Map graph hides media by default** â€” image/font/audio/video endpoints are omitted from the node-link graph; empty folder/host branches are pruned.
- **Map endpoints cache** â€” debounced invalidation (2s) during live capture so the cache is useful under high traffic; immediate invalidation still runs on import/purge.
- **UI perf** â€” debounced inspector find-in-response and activity intent filter; tag palette uses CSS variables for theme consistency.

### Fixed
- **Active XXE built-in Starlark template** no longer uses `SYSTEM file://` entities â€” matches the safe internal-entity Go probe; compile-test guards all built-in active templates.
- **`Hub.SelfAddr` data race** â€” control address is stored in an atomic pointer; active scan and settings rebind no longer race.
- **Settings persist errors surfaced** â€” `PUT /api/settings` and intercept filter saves return 500 when SQLite write fails instead of silently returning cached values.
- **Invalid scope regex rejected** â€” malformed host/path regex patterns return 400 at add/update time instead of silently falling back to literal matching.
- **`humanInput` memory leak** â€” answered prompts are evicted after 60s instead of accumulating for the process lifetime.
- **`ListFindings` N+1** â€” PoC flows for all findings load in one batched query.
- **macOS `interceptor stop`** â€” explicit darwin `List()` via `pgrep` instead of relying on missing `/proc`.

## [0.19.0] - 2026-06-30

**Regex in target scope:** match hosts and paths with patterns like `.*ohsome.*` without giving up `*.acme.com` wildcards.

### Added
- **Target scope regex patterns.** Host and path rules accept regex when the pattern contains metacharacters (e.g. `.*ohsome.*` matches `cdn.ohsome.com`) or is wrapped in slashes (`/pattern/`). Leading wildcards (`*.acme.com`) and exact hosts still work as before.

## [0.18.0] - 2026-06-30

**Control where the UI listens:** change the control-plane port from Settings or the command line â€” no more digging for env vars.

### Added
- **Control UI address in Settings.** Change where the web UI and REST API listen (port or bind address) from **Settings â†’ Proxy & network**, same loopback/external-bind rules as the proxy listener. Persisted as `control.addr`.
- **`--control-port` / `--control-addr` flags.** Set the control UI/API listen address at launch without env vars (e.g. `interceptor --control-port=9967` or `interceptor --control_port=1234`). CLI overrides env and persisted settings for that run.

## [0.17.0] - 2026-06-30

**Usable findings & checks:** attach PoC flows without the proxy-selection dance, jump to any flow by id, and override built-in passive/active checks with editable Starlark.

### Fixed
- **Map tab crash on load.** `renderMap` declared `filtered` twice (endpoint list + filter-active flag); renamed the boolean to `hasFilters`.
- **Findings â€œï¼‹ PoC flowâ€ always rejected.** The button only read Proxy History multi-select (`state.selected`), which plain-click clears; it now falls back to the inspected flow, opens a flow-picker modal when nothing is ready, and shows a â€œÂ· N readyâ€ hint when History selection applies.

### Added
- **Proxy History flow-id search.** Search scope **id** (or `#285` / `id:285` in any scope) finds a flow by its numeric id; command palette and the PoC flow picker match ids too.

### Changed
- **Scanner custom checks list readability.** Wider sidebar, multi-line titles, collapsible built-in/active groups (custom sections open first), and a highlight on the check being edited.
- **Editable built-in scanner checks.** Click any built-in passive check or active probe to view/edit its Starlark template; **Save** writes `~/.interceptor/checks/<id>.star` or `active-checks/<id>.star` and replaces the compiled built-in on the next scan. **Revert** removes your override and restores the default built-in.

## [0.16.0] - 2026-06-30

**Map that scales:** large attack surfaces no longer freeze while you search, and ferox/discovery trash collapses into readable clusters instead of a wall of duplicate rows.

### Added
- **Map response clustering.** Endpoints on the same host that share the latest response body (`res_body_hash`, byte-exact) collapse into one row with a **+N identical** badge; click to expand. HTTP **200 soft-404** pages (body matches a "not found" content signature) cluster separately as **soft-404**. Toggle **Collapsing identical** on the Map toolbar (on by default, persisted like **Hiding 403/404-only**). Nothing is auto-deleted â€” cluster/collapse only.

### Fixed
- **Map search lag on large projects.** Tree rebuild memo no longer keys on the search term; search dims non-matches instead of filtering them out of the tree, so typing stays responsive on 1000+ endpoints. `mapFiltered`, `mapCount`, and `buildGraphTree` are memoized per filter pass.
- **Map `lastStatus` / scheme / body hash pinned to latest flow.** Endpoint aggregation now reads `status`, `scheme`, `res_body_hash`, and `res_len` from the row with `MAX(flows.id)` instead of an arbitrary group member.

## [0.15.0] - 2026-06-30

**Stop what is running:** `interceptor stop` gracefully shuts down every Interceptor instance on the machine (SQLite flush, proxy drain, ports 8080/9966/â€¦), with a 6s grace window before force-kill.

### Added
- **`interceptor stop` subcommand.** Gracefully stops every running Interceptor instance on the machine (flushes SQLite, drains the proxy, frees ports 8080/9966/â€¦), falling back to a force-kill after a 6s grace window. Useful before launching a new version or reclaiming the proxy ports for another app. Reuses the existing signal-driven shutdown path; no daemon or PID file.

## [0.14.0] - 2026-06-30

The **"you can attack that"** release: user-authored active checks, a fully automated agent pentest harness (readiness â†’ discovery â†’ auth â†’ CSRF-aware active scan), Android ADB onboarding, and a unified Checks manager â€” plus a sweep of UI performance and accessibility fixes for large projects.

### Added
- **User-authored ACTIVE attacks (Starlark-for-active).** You can now write your own *active* checks â€” not just passive ones. A custom active check is a `def check(point, baseline, probe)` Starlark script where `probe(payload)` sends a real mutated request through the engine (recorded, session-auth applied, counts against the run's budget) and `finding(...)` reports a confirmed vuln. Fully sandboxed (no files/sockets/imports, step-bounded). New `internal/activescript` package; REST CRUD + a **Test** endpoint (`GET/PUT/DELETE /api/active-checks[/{id}]`, `POST /api/active-checks/test`); the Checks manager gains a **CUSTOM Â· ACTIVE** section with create/edit/delete/test (the AI-Describe tab is passive-only). Custom active checks run alongside the built-in probes when you arm & run an active scan, and are toggleable in the same manager (namespace `custom-active:` so they never collide with built-in IDs).
- **Unified Scanner-checks manager.** The Checks modal now lists *every* module in one place: **18 built-in passive checks** and **9 active-scan probes** (all individually **toggleable** on/off via one shared `checks.disabled` setting), plus your **custom Starlark checks** (full create/edit/delete + AI-generate). Active probes are tagged âš¡ "sends traffic" and fire only when you arm & run an active scan â€” the *management* UI is unified, but active *execution* stays consent-gated because it sends real attack requests. `GET /api/checks` returns `builtin` + `active` (each with a stable `id`) alongside `checks`/`disabled`; the disabled set is honoured by the passive scan, single-flow analysis, AI context, **and** the active engine.
- **New passive detection: possible SQL injection.** Responses containing a database error string (`SQL syntax`, `ORA-`, `SQLSTATE[â€¦]`, `sqlite3.OperationalError`, `pg_query failed`, "unclosed quotation mark", `System.Data.SqlClient.SqlException`, â€¦) now produce a **High** "Possible SQL injection (DB error in response)" finding â€” a strong passive SQLi signal (error-message phrasing only, to keep false positives low).
- **New passive detection: internal IP disclosure** â€” a private/loopback/link-local IP in a response body yields a **Low** topology-leak finding.
- **First-run setup wizard.** A 4-step guide (point at the proxy â†’ download & trust the CA with OS-specific instructions â†’ add a target-scope host â†’ done) shown once on first boot, and reopenable from **Settings â†’ Project & data â†’ Run setup wizard again**. Auto-skips for returning users who already have captured traffic.
- **Scanner â†’ Findings bridge.** Each passive-scan issue group has a **âž• Promote to Finding** action that creates a curated finding (title/severity/detail/fix) with every PoC flow attached, then opens it â€” the two views of "vulnerabilities" are no longer disconnected silos.
- **Docs & examples for custom active checks.** The user-authored active-checks feature now ships a reference page ([`docs/custom-active-checks.md`](docs/custom-active-checks.md) â€” the active twin of the passive `docs/custom-checks.md`), two ready-to-copy examples in [`examples/active-checks/`](examples/active-checks/) (error-based SQLi, reflected XSS), and cross-links from the passive docs and the in-app **Docs** tab. A build-tagged test (`go test -tags examples ./examples/active-checks`) compiles every shipped example so docs edits can't silently ship a broken `.star`.
- **WebSocket click-to-replay.** Captured text frames are now click-to-load into the replay box (the most-expected WS affordance that was missing); the replay headline no longer misleads with a raw "HTTP 200".

### Changed
- **Security-header noise collapsed 5 â†’ 1.** Missing CSP, HSTS, X-Content-Type-Options, clickjacking protection, and Referrer-Policy previously each emitted a *separate* finding (a single HTML page could produce five near-duplicates). They now merge into one **"Missing security response headers"** finding that lists which are missing â€” Medium when CSP or HSTS is among them, otherwise Low.
- **Intruder tab redesigned for clarity.** The attack bar is now grouped by intent â€” `target Â· mode Â· runtime Â· Start` â€” with thin separators between groups, instead of an undifferentiated river of 11 controls. **Â§ Mark** moved into the Request Template header (where you edit), the **History** toggle moved into the Results header (past runs live with results), and the advanced result-processing fields (**flag / extract / process**) plus **presets** folded behind an **Options â–¾** disclosure with consistent labels. The primary path is now just: target â†’ mode â†’ Start.
- **Leaner Session/auth surface (progressive disclosure).** The common case is now "paste a token + Save": global session headers stay visible, while per-host overrides, the token macro, and the login macro each collapse behind a disclosure. The three redundant per-block Save buttons are gone â€” one **Save session** persists all four mechanisms (and toasts macro completeness / enablement).
- **Scanner toolbar splits passive vs offensive.** The safe passive controls (Run scan, Custom checks) sit on the left; **Active scan** and **OOB** move behind a divider labelled **OFFENSIVE** on the right, so the safe/unsafe boundary is visible at a glance.
- **Proxy toolbar de-cluttered.** Dropped the rarely-used **all/https/http** scheme selector (filterable via search/tags instead), removing the leftmost toolbar slot.
- **Intruder preset save uses the themed modal** instead of a blocking browser `prompt()`.
- **Map search is O(N), not O(NÂ²).** "Expand-to-search" now marks matches in a single bottom-up pass instead of re-scanning every subtree at every node, so typing in the Map search no longer freezes large projects.
- **Leaner, clearer toolbars (progressive disclosure).** Discover folds its 8 advanced knobs (extensions, recursion, threads, delay, soft-404, record, tag-API) behind an **Options â–¾** disclosure, leaving Base URL + Start + sources as the default surface. The Intruder attack bar collapses 5 named modes into **Sniper / Lists â–¾ / Repeat**, with Battering/Pitchfork/Cluster chosen by a sub-select, and **wraps** instead of overflowing at 1280px. The Map Graph view now offers a **show graph anyway** link instead of a flat refusal past the 200-node cap.
- **Map hides forced-browse noise by default.** Endpoints that *only* ever returned **403** or **404** are filtered out (ferox/discovery dead paths). Paths that later return 2xx, 401, 5xx, etc. stay visible. Toggle **Hiding 403/404-only** on the Map toolbar to show everything (`?hideNoise=0` on the API).
- **Findings export report.** Curated findings only by default (passive-scan appendix dropped â€” use `?issues=1` or MCP `includeIssues=true` to add it back). Format selector on the Findings tab: **Markdown**, **HTML** download, or **PDF** via print dialog.
- **Android Wiâ€‘Fi proxy hint.** When Wiâ€‘Fi mode needs LAN bind, a **Settings â†’ Proxy** button jumps to the proxy listener section (does not change bind for you).
- **Themed dropdowns app-wide.** All native `<select>` controls use the custom in-app menu (filters, Repeater method, Settings, scope/rules tables, Findings, etc.) instead of the OS picker; dynamically added selects are upgraded automatically.
- **Android ADB device picker UI.** Custom themed device menu (no OS-native dropdown) and USB/Wiâ€‘Fi segmented control; shows model name with serial/transport subtitle.
- **Findings detail reads like a report.** The finding detail pane now renders as a narrative document â€” prose paragraphs for text blocks, blockquote-style PoC callouts for flows (matching the exported Markdown report), hover-revealed edit controls, and a dedicated Impact callout â€” instead of boxed table-like blocks with FLOW/TEXT labels.
- **Findings text blocks edit in place.** Narrative paragraphs render as markdown at rest; click to edit overlays a same-size field so the layout doesn't jump, then re-renders on blur.
- **Findings Chain view.** The finding detail pane has a **Report | Chain** toggle: Chain renders the ordered body blocks as a vertical attack-step timeline (numbered rail, full narrative text, PoC callouts, Impact tail) instead of a cramped horizontal SVG graph.

### Added (continued)
- **Android ADB setup (Settings â†’ TLS).** When `adb` is on PATH: install CA (user prompt, system/root, or `auto` for emulators), proxy via USB (`adb reverse`) or Wiâ€‘Fi (LAN IP), **Setup all** (proxy + CA), clear proxy with optional system CA removal. MCP: `android_status`, `android_setup`, `android_teardown`. REST: `GET /api/android/status`, `POST /api/android/setup`, `POST /api/android/install-ca`, `POST /api/android/proxy`, `POST /api/android/unproxy` (`internal/android`).
- **Agent pentest automation (P0â€“P2).** `check_readiness` v2 returns structured JSON blockers (OOB, auth identities, login macro, scope, traffic). `start_discovery` falls back to the built-in wordlist when empty (optional history seeds). New MCP tools: `get_flow_auth`, `promote_flow_to_authz`, `set_login_macro_from_flow`, `set_login_macro`, `test_login_macro`, `get_discovery_wordlist`, `oob_enable`. JWT extract v2 (`internal/auth/jwtextract`) for Bearer/path/JSON/query/cookie tokens; `cross_host_token_replay` accepts `mode:auto|bearer|path`. CSRF-aware active scan (Laravel XSRF bootstrap, default `csrfAware:true`) with per-endpoint circuit breaker (skip after repeated 419/401/403/502). REST: `GET /api/readiness`, `POST /api/authz/from-flow/{id}`.

### Accessibility
- **Keyboard access to previously mouse-only controls.** History tag chips (filter by tag), sortable column headers (Proxy + Map), the Map's tree endpoints / table rows / params rows, and the Scanner custom-check list rows are now focusable and operable via Enter/Space with correct ARIA roles. Repeater and Intruder tab-close "âœ•" are real `<button>`s (keyboard-closeable). `wireRowKey` now defers to any focused child promoted to `role="button"`, so nested controls no longer double-activate. (The Map's SVG node-link graph remains pointer-driven â€” Tree/Table/Params are the keyboard-accessible equivalents.)
- **Saved Views â†’ one dropdown.** The Proxy toolbar's apply/save/delete-view triplet is now a single **Views â–¾** menu (count badge, apply, save current, delete) â€” removes a 1280px-overflow trigger.
- **Authz three-mode picker.** The authorization modal replaces its target radios + floating **Cross-host** button with one **Selected flow | All in-scope | Cross-host JWT** segment that retargets Run and reveals only the controls each mode needs.
- **Findings view labels.** **Report â†’ Edit** and **Chain â†’ Timeline** (clearer that the second is read-only). Intruder **Null â†’ Repeat**, the `encode` field label â†’ **process**, and Discover **Hide len â†’ Soft-404 len**.
- **Inspector Render button is HTML-only.** It's hidden for non-HTML responses (where it used to silently fall through to an ugly raw view) and falls back to Pretty. Find-in-page now reports a live match count in the previously-empty status span.
- **AI agent toggle is provider-aware.** "Let AI send requests" disables itself under OpenRouter (it's Anthropic-only) instead of silently no-op'ing mid-run.

### Removed
- **Dead/redundant UI.** `#aiPulse` (redundant with the Activity tab badge), the Activity "All / With intent" segmented toggle (the free-text intent filter covers it), the Proxy **AI** source-filter button (tag-bar filtering covers it), the Intercept **Apply** button (auto-apply + Enter already commit), the duplicate cloudflared-tunnel block in Settings â†’ Scanner (kept in the OOB modal), the duplicate **Export report** button on the Scanner tab (Findings is the single source of truth now), the no-op `setScanSub` legacy shim, and the request-side Raw/Pretty toggle in Repeater (compact-on-send already normalizes).

### Fixed
- **History jank under heavy traffic.** In virtualized mode (â‰¥120 flows) every `flow.new`/`flow.update` event triggered a full window rebuild (`renderRows`) â€” under a busy proxy that was dozens of synchronous rebuilds per second. Rebuilds are now coalesced via `requestAnimationFrame` (many events per frame â†’ one render). Separately, the method filter `<select>` was rebuilt on every event after scanning all loaded flows; it's now rebuilt only when a genuinely new method appears (tracked incrementally).
- **Intruder results jank on large attacks.** The results list rebuilt every row (potentially thousands) on each 120 ms poll while running. It's now virtualized above 200 rows â€” only the visible window is rendered, repainted on scroll (same pattern as the Map table and Proxy rows).
- **Map tab lag on large datasets.** Three causes fixed: (1) `buildMapTree` was rebuilt on every render â€” including 2â€“3Ã— per search keystroke â€” so typing in Map search with thousands of endpoints rebuilt the whole in-memory tree repeatedly; it's now memoized on data-version + active filters and reused across renders. (2) The host `<select>` (`All domainsâ€¦`) was rebuilt with potentially thousands of options on every re-fetch; it's now rebuilt only when the host set changes. (3) On a busy proxy the SSE stream re-fetched + re-rendered the entire map every 900 ms while the tab was open; it now debounces to 2 s and skips the re-fetch entirely when no new flows have arrived.
- **UI audit fixes (data-loss + correctness).** Findings body-save race that could PATCH one finding's blocks onto another after a fast switch (now snapshots blocks at schedule time, and the detail pane skips rebuilding while a text block is mid-edit, so an SSE round-trip can't wipe the focused textarea). PDF export was dead â€” `window.open(...,'noopener')` returns `null` per spec, so the print path never ran (dropped `noopener`). Intercept could Forward a different item's body than the editor showed after a fast second click (re-checks `heldSel` after the raw fetch). Repeater/Intruder tab races: `renderRepResponse`/`loadRepHistory`/`repLoadSend`/`sendToIntruder` now guard against a tab switch during an `await` so stale data can't land in the wrong tab. Find-in-page in the response inspector no longer corrupts the syntax-highlight markup (marks now match only inside text runs, never inside tags). Rename/status/delete now toast and update optimistically. Plus: non-virtualized History list no longer rebuilds on every scroll tick (rAF-throttled + skipped when fully rendered), the retention "select all" checkbox tracks per-row changes (`indeterminate`), and a dead `'change':'change'` ternary in match-&-replace wiring was removed.
- **Android ADB device list with spaced serials.** `adb devices -l` rows like MuMu's `(no serial number)     device â€¦` are parsed correctly (serial was split on spaces, so Interceptor reported "no authorized device" despite a connected device). Commands use `adb -t transport_id` (or the default device when only one is connected) because adb rejects `-s "(no serial number)"`.
- **Ctrl+R / Ctrl+I no longer fire outside Proxy History.** Flow send shortcuts now require the Proxy History tab (and ignore keypresses while typing in inputs). Fixes Ctrl+R in Settings still sending the previously selected flow to Repeater.
- **Findings list/detail empty after feature update.** Empty findings no longer serialize `blocks`/`flows` as JSON `null` (always `[]`), the list auto-selects the first finding, and row meta shows step count / "needs content" for bare entries. Bounty-project data repaired via `scripts/fix-findings-bounty.ps1` â€” interleaved `body` blocks, `impact` populated from legacy `fix`, and corrupted finding #6 (SSO redirect) restored.

## [0.13.0] - 2026-06-29

### Changed
- **Findings is now its own top-level tab.** Promoted out of the Scanner tab into a standalone **Findings** menu (the Scanner tab is now passive-issues only) â€” findings are first-class, not a sub-view. Flow cross-links ("open finding") and the saved-tab restore now target the new tab.
- **Findings define Impact instead of Remediation.** A finding now captures its **security impact** (what an attacker gains / business consequence) via a new `impact` field, replacing the old "Remediation" field on curated findings. Shown in the finding detail pane and rendered as `**Impact:**` in the exported report. Passive scanner-issue remediation is unchanged. Stored in a new additive `impact` SQLite column; exposed on create/update (REST + MCP), with legacy `fix` still accepted for back-compat.
- **MCP records findings description-first.** `create_finding`/`update_finding` now take `impact` (in place of `fix`), and the `initialize` methodology mandates the workflow: write a finding's **description and impact first**, then **always attach the relevant captured flow(s) as PoC** via `add_finding_poc` â€” every finding should have a description before evidence and at least one PoC flow when one exists.

### Added
- **`cvss` field on findings.** Findings now carry a dedicated `cvss` field (score or vector, e.g. `9.8` or `CVSS:3.1/AV:N/...`) instead of embedding it in the title â€” stored in a new additive SQLite column, accepted on create/update (REST + MCP), rendered as `**CVSS:**` in the report, and editable in the finding detail pane.
- **`add_finding_poc` position param.** The MCP tool and `POST /api/findings/:id/flows` accept an optional 0-based `position` to insert a PoC flow block at a specific index in the narrative body (omit = append).
- **`list_flows` tag filter.** The MCP `list_flows` tool and `GET /api/flows` accept a `tag` argument to filter flows by tag (e.g. `tag:auth`).
- **`create_finding`/`update_finding` return a UI deep-link.** Their MCP results include `â€¦/#finding-<id>`; navigating to that hash in the web UI activates the Findings tab and selects the finding.
- **Auto-tag auth flows.** Captured flows whose request path looks like an auth endpoint (`/login`, `/register`, `/logout`, `/auth`, `/oauth`, `/token`, `/sso`, `/mfa`, `/2fa`, `/password`, `/reset`, `/verify`, â€¦) are automatically tagged `auth` (segment-exact, false-positive-guarded), surfacing the auth surface for instant `tag:auth` filtering. Best-effort in the capture/sender/proxy persist path; never blocks forwarding.
- **`create_finding`/`update_finding` accept a `body` param over MCP.** Lets an agent set the full interleaved block structure directly (previously only reachable via raw REST).
- **Track `.cursor/mcp.json` in the repo.** The documented Cursor MCP config (Streamable HTTP to `http://127.0.0.1:9966/mcp`) is now checked in so a fresh clone connects Cursor to a running Interceptor with no manual setup.
- **Project `.mcp.json` for Claude Code.** Checks in the Claude Code MCP config (Streamable HTTP to `http://127.0.0.1:9966/mcp`) so Claude Code connects to a running Interceptor â€” the Claude-Code analogue of `.cursor/mcp.json`.

### Fixed
- **`normalizeFindingSeverity` no longer downgrades "critical".** A severity of `critical` (any case) was silently mapped to `Medium`; it now normalizes to canonical `Critical` (matching the report's severity ranking).
- **Updating a finding's `detail` preserves interleaved bodies.** A `detail`-only update now replaces just the first text block in place, keeping every flow block in its original position â€” previously it could reorder/append flows and break the interleaved narrative.
- **Project create/switch on Windows.** Switching or creating a project from the web UI did nothing on Windows: the re-exec used `syscall.Exec`, which Windows doesn't implement (it returns "not supported by windows"), so the process never restarted on the new project. The re-exec is now platform-specific â€” `syscall.Exec` (in-place image swap) on Unix, spawn-a-fresh-process-and-exit on Windows â€” with a gated `listenRetry` so the spawned child reclaims the proxy/control ports once the old process releases them (a normal start still fails fast on a genuinely taken port). Verified live on Windows: creating a new project re-execs and lands on it.

## [0.12.0] - 2026-06-28

### Added
- **`diff_flows` capability.** New MCP tool `diff_flows` and `GET /api/flows/diff?a=&b=` endpoint compare two captured flows' responses â€” status change, response-length delta, header add/remove/change, and a bounded line-based body diff. Lets an AI confirm whether a payload actually changed the response (baseline vs exploit). Body comparison is byte-capped like other tools.
- **Four more passive-scan checks (17â€“20).** Missing `Referrer-Policy` on HTML responses (Low), mixed content on HTTPS pages (`http://` script/style/img/iframe, Medium), potential open redirect via a request parameter reflected in a 3xx `Location` (Medium, off-host only), and directory-listing exposure via the autoindex title pattern (Low) â€” each conservatively gated with positive/negative tests.
- **Intruder anomaly auto-flagging.** Results are auto-flagged when their status differs from the modal status or their response length deviates from the median (Â±20% / 50-byte band), plus a grep-minority signal; new `anomaly` field on results and an amber `âˆ¿` highlight on outlier rows so interesting responses stand out instantly.
- **Store retention/query test hardening.** Strengthened `internal/store` coverage (68.7% â†’ 74.1%): GC shared-hash body dedup (a body shared by two flows survives while one flow remains), keyset pagination across all non-id sort keys, `QueryFlowsFilter` combinators, finding-body helpers, and `Missing`-flag propagation through host pruning. No bugs found â€” invariants held.
- **`crlf` active-scan check.** CRLF-injection / HTTP-response-splitting probe (High severity) injecting CR/LF in five encodings (raw, URL-encoded uppercase/lowercase, double-encoded); confirms via the injected header (or `Set-Cookie`) appearing in the *response headers* rather than body reflection, with a baseline guard.
- **Stale PoC evidence flagging.** When a finding's PoC flow is purged from history (prune/GC), the body block and its annotation are preserved and shown as a dimmed "âš  evidence deleted â€” re-capture this endpoint" badge (with a per-finding banner) in the UI and a "âš  PoC flow #N â€” evidence no longer in history" note in the exported report, instead of a silently empty/broken PoC.
- **Soft-404 auto-calibration in discovery.** Forced-browse fires 3 random-path probes per directory before each wordlist sweep; if they return a consistent fingerprint (status + body length within ~5%/32 bytes) it suppresses wordlist hits that match that baseline â€” killing soft-404 false positives. Best-effort (falls back to no suppression on error); opt out with `Spec.DisableSoft404Calibration`.
- **Four new passive-scan checks.** CORS-with-credentials (both the `ACAO: *` wildcard and the reflected-Origin variant, High), sensitive token/credential in the request URL (Medium), `Set-Cookie` missing `SameSite` (Low), and authenticated responses that shared proxies may cache (Set-Cookie without `no-store`/`private`, Low). All conservative, with positive/negative tests.
- **Better MCP argument errors.** MCP tool argument-validation now reports the expected type and the offending value (e.g. `flowId must be an integer (got string "abc")`), truncated to 60 chars with secret-named fields masked â€” so an AI agent can self-correct instead of looping on a bad call.
- **`xxe` active-scan check.** XML request bodies are now enumerated as a `body/_xml` injection point and probed for in-band XML External Entity injection using a safe internal-entity canary (`<!ENTITY xxe "INTERCEPTOR_XXE_CANARY">`) â€” no external/SYSTEM file-read entities. Flags High severity when the entity resolves in the response, with a baseline false-positive guard. Skips non-XML requests.
- **Discovery auto-tags API endpoints.** Forced-browse hits whose path looks like an API (`/api`, `/graphql`, version segments like `/v1`, `.json`/`.xml`, etc.) are automatically tagged `api`, with a static-asset veto so `.css/.js/.png/â€¦` aren't tagged. Default-on **Tag APIs** toggle in the Discovery bar; tagging is best-effort and never breaks a run.
- **Activity feed intent filter.** The Activity tab gains an **All / ðŸ’­ With intent** toggle (show only actions where the AI stated a reason) plus a free-text intent substring filter â€” client-side over the loaded feed, preserving workflow-separator grouping on the filtered subset.
- **Finding body size cap.** Finding narrative-body writes are capped at 1 MiB total (HTTP 413) and 256 KiB per text block, enforced on both the REST and MCP create/update paths, to prevent runaway-AI or malicious storage bloat.
- **"Send as" context-menu action.** Right-click any flow in Proxy History (or the inspector pane) â†’ **SEND AS** section lists every saved authz identity. Clicking one loads the flow into a new Repeater tab with the selected identity's auth headers (Cookie/Authorization) substituted â€” one click to replay a captured request as any test role. The identity list is cached from Settings and refreshed whenever identities are saved.
- **Broken account annotation.** Each authz identity now has a **âš ** toggle button in the Authorization modal. Marking an identity broken (e.g., after a lockout) dims its row, adds a warning badge, excludes it from **SEND AS**, and causes it to be **skipped** (not replayed) in `authz_run` and `cross_host_token_replay` â€” its result row shows "âš  broken â€” skipped" instead of live results. Check sessions also skips broken identities. The `broken` flag is persisted with the identity JSON.
- **Findings narrative body.** Each finding now has an interleaved document body instead of separate "Detail / Evidence" text areas and a flat PoC list at the bottom. The body is a free-order sequence of **text blocks** (markdown) and **flow blocks** (clickable PoC request/response badge with an annotation field). Add text with **ï¼‹ Text**, attach selected History flows with **ï¼‹ Flow**; reorder with â†‘/â†“; delete with âœ•. Content auto-saves on blur. Existing findings (detail + attached flows) are transparently migrated to blocks on first read â€” no data loss. The export report renders blocks in author order (text paragraphs interleaved with `> GET host/path â†’ STATUS` flow quotes). MCP backward-compat preserved: `update_finding(detail=...)` updates the first text block; `add_finding_poc` appends a flow block; `list_findings` syncs detail from the first text block.
- **Authorization test matrix view.** The authz results area now has a **List / Matrix** toggle for bulk runs. Matrix view shows a single table with endpoints as rows and identities as columns, with per-cell status and size â€” far easier to scan when testing many endpoints Ã— roles. `âš  same access` flags highlight the row.
- **Cross-host JWT replay.** New **â†” Cross-host** button in the Authorization modal (and MCP tool `cross_host_token_replay`). Extracts the Bearer token from the selected flow and replays the same path to every unique in-scope host in history â€” automates detection of cross-environment JWT confusion (shared secret / same-secret multi-tenant bugs). Results show accepted/rejected per host with a link to the captured flow.
- **Per-host session headers.** The Session module now supports host-specific auth overrides alongside the global headers. Set a different Authorization/Cookie per hostname â€” when a send target matches, the host override replaces the global headers for that request. Exposed via Settings â†’ Session / Auth (UI table with `+ Add host` rows), the `set_session` API (`hostHeaders` field), and the MCP `set_session` tool (`hostHeaders` JSON object). Eliminates the friction of testing multiple targets simultaneously (the main use case: two auth domains, one project).
- **JWT expiry countdown in session UI.** Settings â†’ Session now shows a live expiry timer (`Expires in Xh Ym`) parsed from the Bearer token in the global session headers. Turns amber under 30 minutes and red under 5 minutes. Refreshes every 30 seconds.

### Fixed
- **Intruder grep on compressed responses.** Grep-match and grep-extract now decompress `gzip`/`deflate`/`br`/`zstd` response bodies before matching (previously an encoded body silently matched nothing). Genuinely binary responses (`image/*`, etc.) are skipped and flagged `binary` on the result instead of failing quietly. Decompression logic was consolidated into a shared `internal/codec.DecompressBody` (also used by the response viewer).
- **History live refresh for MCP/tool sends.** Repeater, Intruder, active scan, and discovery sends now broadcast `flow.new` over SSE (via `sender.SetOnPersist`) so Proxy History updates live for AI/MCP traffic â€” not only proxied browser traffic. Virtualized History (â‰¥120 rows) re-renders on incremental updates instead of patching a single DOM row.

### Added
- **Intruder Battering ram + Cluster bomb.** New attack types matching Burp: `battering` applies one payload list to every Â§ marker at once; `cluster` runs the cartesian product of per-marker lists. UI attack bar, MCP `start_intruder`, and tests.
- **Intruder attack presets.** Save/load attack setups in the Intruder bar via localStorage (`presetsâ€¦` / ðŸ’¾).
- **History row virtualization.** Proxy History virtualizes the flow list when â‰¥120 rows are loaded â€” only visible rows stay in the DOM.
- **Inspector find-in-response.** `Ctrl+F` on the Proxy tab opens a find bar on the response pane with match highlighting.
- **Response Render tab.** HTML responses preview in a sandboxed iframe (Raw / Pretty / Render).
- **`internal/httplines`.** Shared header normalizer for Repeater/MCP â€” accepts `"Key: Value"` lines or a JSON object (fixes MCP agents passing `headers` as a map).
- **MCP `flowId` alias.** `analyze_flow`, `get_flow`, and related tools accept `flowId` as well as `id`.
- **MCP Cursor auto-sync.** Project `.cursor/mcp.json` uses Streamable HTTP (`http://127.0.0.1:9966/mcp`) so MCP matches the running Interceptor after restart â€” no stale stdio subprocess. `scripts/interceptor-mcp` resolves the latest binary for stdio clients; `interceptor update` prints an MCP restart hint.
- **In-scope history pagination fix.** `?inScope=1` pages until enough in-scope rows are found; `GET /api/flows/inscope` for readiness checks.
- **Param miner.** `GET /api/params` aggregates query, form, and JSON keys from captured traffic; Map tab **Params** view lists them per host with send-to-Intruder shortcuts.
- **OOB tunnel helper.** OOB modal and Settings â†’ Scanner show a copy-paste `cloudflared` one-liner for a public callback URL.
- **Ask AI agent mode (opt-in).** The âœ¨ Ask AI modal has a per-session **Let AI send
  requests** toggle (default off). When enabled with Anthropic as the provider, the
  model can call `send_request` and `get_flow` (up to 5 tool steps per question) to
  probe URLs via Repeater â€” cookies/auth headers are seeded from the selected flow.
  Tool steps appear as Tool bubbles in the thread; the final answer still streams over
  SSE (`event: tool` during the loop, then `data:` text chunks).
- **Ask AI follow-up questions.** The âœ¨ Ask AI modal now keeps a conversation thread:
  ask a question, read the streamed answer, then ask follow-ups in the same panel
  (prior Q&A is sent as `history` so the model stays in context). The thread renders
  as You / AI bubbles; **â†º New chat** clears it; **Copy** grabs the whole exchange.
- **Tag removal in History.** Right-click a flow (TAGS section) or a tag chip to
  remove one tag from that flow â€” or from every row in a multi-selection. Bulk
  `POST /api/flows/tags` now accepts `"remove": [...]` alongside `"add"`. MCP:
  `untag_flow`.
- **Active scan request log.** Every probe is recorded as a `FlagActiveScan` flow
  (including transport errors as 502) whether or not it confirms a finding.
  `GET /api/activescan` includes a live `logs` array for the current run;
  `GET /api/activescan/history` lists all saved probes. The âš¡ Active scan modal
  shows a **Request log** panel (click a row to inspect).
- **Intruder file payloads â€” preview only in UI.** Loading a wordlist with ðŸ“‚/ï¼‹
  keeps the full list in memory for the attack but shows only the first 40 lines
  in the editor (readonly). Counts and Start still use the full list; huge lists
  are not written to localStorage.

### Changed
- **Engagement report quality.** `internal/report` now orders findings by an explicit severity rank (Criticalâ†’Highâ†’Mediumâ†’Lowâ†’Info), adds an executive-summary table (counts by severity and by status), and moves `false_positive` findings out of the main body into a dedicated "Excluded â€” False Positives" section. Deterministic ordering; PoC rendering and the passive-scan appendix unchanged.
- **Session injection is scope-gated by URL path** (not just host) â€” Repeater/Intruder sends only attach auth headers when the target URL matches scope rules; `session.unscoped` opt-in still sends everywhere (unsafe).
- **System font stack** replaces Google Fonts JetBrains Mono â€” works offline/air-gapped.
- **Windows proxy onboarding** in the get-started card (`Settings â†’ Network â†’ Proxy` / `netsh winhttp`).
- **Scope duplicate warning** in Settings â†’ Target scope when identical rules exist.
- **History source filters: Manual + AI toggles.** Replaced the confusing **ðŸ¤– AI** /
  **ðŸ¤– only** pair with independent **Manual** and **AI** buttons (both on by default).
  Enable both to see proxy + bot traffic; disable one to see only manual captures or
  only AI sends. API: `?manual=0|1&ai=0|1` (`?onlyAi=1` still works for AI-only).
- **History sort is server-side.** Column headers send `?sort=&dir=` to the API
  (keyset pagination via `curId`/`curVal`, legacy `before=` for id DESC). Sorting
  no longer reorders only the loaded browser page â€” id ascending starts at flow #1
  and infinite scroll fetches the rest. Initial fetch loads 250 rows plus a 50-row
  buffer to reduce scroll lag.
- **History uses infinite scroll + virtualization.** Loads older flows on scroll;
  lists â‰¥120 rows virtualize so the DOM stays bounded.
- **AI assist is now a single "Ask AI" question box** instead of the Explain /
  Payloads / Summary preset modes. Open it on a flow (or a multi-selection) and ask
  anything about the captured request/response â€” "is the CSRF token validated?",
  "what auth scheme is this?", "suggest test payloads" â€” and the answer streams in,
  grounded in the exchange. Simpler and more flexible (one box does what the three
  presets did). Backed by `kind:"ask"` + `question` on the assist endpoints; the
  preset segmented control and the structured-payload cards were removed.
- **Scanner tab absorbs Findings.** Passive scan issues and curated findings share
  one tab with a **Passive / Findings** toggle â€” fewer top-level tabs.
- **API & MCP moved to Settings.** API keys, REST reference, and MCP config live
  under Settings â†’ **API & MCP**; the standalone API tab is removed.
- **Comparer upgrade.** Two-flow compare now diffs response headers and bodies with
  word-level highlights; body size cap raised to 512 KiB per side.

### Fixed
- **MCP `send_request` with object headers** no longer produces corrupt `map[User-Agent` header names.
- **`check_readiness` / in-scope filter** no longer false-negative when in-scope traffic exists but recent rows are telemetry/noise (`GET /api/flows/inscope`).
- **Sender `port` use-before-declare** compile bug in session scope gating path.
- **History Ctrl/Cmd-click multi-select kept only the second row.** A plain click
  inspects a row but doesn't add it to the multi-select set, so Ctrl/Cmd-clicking a
  second row selected only that one. Ctrl/Cmd-click now seeds the set with the
  currently-inspected row first, so both the originally-clicked row and the
  Ctrl-clicked one end up selected.

### Removed
- **Show in Map.** Removed the History/inspector context-menu item and **Ctrl+M** /
  **âŒ˜M** shortcut that jumped to the Map tab filtered to the selected flow. **Search
  in Map (body)** (inspector text selection) and the Map tab itself are unchanged.
- **History "Export" / "Import" (HAR) toolbar buttons.** Removed from the Proxy
  History toolbar (unused in practice). The `/api/export/har` and `/api/import/har`
  endpoints are unchanged, and full project export/import remains in Settings.
- **History "ðŸ”Ž discover" filter button.** It only showed content-discovery hits,
  which are already marked with a "DSC" badge on their rows and findable via the
  Discover tab â€” so the toolbar toggle was redundant. The `?discovery=1` API filter
  on `/api/flows` is unchanged.


