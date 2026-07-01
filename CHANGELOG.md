# Changelog

All notable changes to **Interceptor** are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).


> **Archive:** Release notes for 0.11.0 and earlier live in [CHANGELOG/archive/pre-0.12.md](CHANGELOG/archive/pre-0.12.md).

## [Unreleased]

## [0.21.0] - 2026-07-01

### Changed
- **External bind allowed by default.** Rebinding the proxy or control UI to `0.0.0.0` (or any non-loopback address) no longer requires `INTERCEPTOR_ALLOW_EXTERNAL_BIND=1`. Default listen addresses stay loopback; set `127.0.0.1` in Settings to stay localhost-only. Set `INTERCEPTOR_ALLOW_EXTERNAL_BIND=0` to lock down non-loopback rebinding.

### Added
- **SSL pinning / TLS MITM failure detector.** When a mobile app sends CONNECT but rejects the proxy's leaf certificate, Interceptor now records a `FlagTLSFailed` flow (tagged `tls-failed`, `ssl-pinning?`) instead of silently dropping the tunnel. New `GET /api/tls-diagnosis` and MCP `detect_ssl_pinning` distinguish **tls_blocked** (pinning or untrusted CA) from **no_traffic** (proxy bypass) and **no_https** (cleartext only). `check_readiness` adds a `tls_intercept` blocker; History shows a red **PIN** badge on failed handshakes. UI: live banner in Proxy History + Settings → TLS → SSL pinning section (explicitly states Interceptor cannot bypass pinning — device-side Frida/APK patch required).
- **iOS automation (Settings → TLS → iOS).** Simulator: install CA via `simctl keychain add-root-cert` and open a `.mobileconfig` (CA + global HTTP proxy) in Safari. Physical iPhone: download/serve profile at `GET /api/ios/profile.mobileconfig` — install in Safari, enable full trust. MCP: `ios_status`, `ios_setup`, `ios_install_ca`. Optional `libimobiledevice` for USB device listing. Does not bypass SSL pinning (same as Android).

### Fixed
- **`interceptor update` GitHub 403** — update check sends a proper `User-Agent`, falls back to the public `/releases/latest` redirect when the API is rate-limited, and accepts `GITHUB_TOKEN` for authenticated quota.

## [0.20.0] - 2026-06-30

### Added
- **CI workflow** (`.github/workflows/ci.yml`) — `go vet`, `go test`, `go test -race`, and cross-platform `CGO_ENABLED=0` builds on every push/PR.
- **Release workflow** (`.github/workflows/release.yml`) + **GoReleaser** (`.goreleaser.yaml`) — multi-arch binaries and `checksums.txt` on `v*` tags.
- **`internal/hostpattern`** — shared `*.wildcard` / exact host matching for retention purge and scope rules.
- **`internal/strutil.AtoiOr`** — single implementation replaces copy-pasted `atoiOr` in proxy/sender/control.
- **`internal/httplines.ParseRawRequest`** — shared raw HTTP request parsing for Repeater, Intruder, and login macro.
- **`breaker` package tests** — concurrent circuit-breaker coverage for the active-scan engine.
- **`apiTry()` helper** — optional toast-on-error wrapper for fire-and-forget UI `api()` calls.

### Changed
- **`interceptor update` progress.** Step-by-step status (check → download → verify → extract → install) plus live download percentage and size when the terminal supports it.
- **Control API layout** — domain-scoped route registration (`routes_register.go`, `api_types.go`); HTTP handlers grouped by `flowAPI`, `interceptAPI`, `settingsAPI`, etc.
- **Scope host matching** — wildcard/exact host rules delegate to `hostpattern.MatchHost`.
- **CHANGELOG** — pre-0.12.0 release notes archived to `CHANGELOG/archive/pre-0.12.md`.
- **`apiTry()` rollout** — Map endpoints loader and Scanner active-scan/OOB polls use the helper instead of silent `catch`.
- **Map graph hides media by default** — image/font/audio/video endpoints are omitted from the node-link graph; empty folder/host branches are pruned.
- **Map endpoints cache** — debounced invalidation (2s) during live capture so the cache is useful under high traffic; immediate invalidation still runs on import/purge.
- **UI perf** — debounced inspector find-in-response and activity intent filter; tag palette uses CSS variables for theme consistency.

### Fixed
- **Active XXE built-in Starlark template** no longer uses `SYSTEM file://` entities — matches the safe internal-entity Go probe; compile-test guards all built-in active templates.
- **`Hub.SelfAddr` data race** — control address is stored in an atomic pointer; active scan and settings rebind no longer race.
- **Settings persist errors surfaced** — `PUT /api/settings` and intercept filter saves return 500 when SQLite write fails instead of silently returning cached values.
- **Invalid scope regex rejected** — malformed host/path regex patterns return 400 at add/update time instead of silently falling back to literal matching.
- **`humanInput` memory leak** — answered prompts are evicted after 60s instead of accumulating for the process lifetime.
- **`ListFindings` N+1** — PoC flows for all findings load in one batched query.
- **macOS `interceptor stop`** — explicit darwin `List()` via `pgrep` instead of relying on missing `/proc`.

## [0.19.0] - 2026-06-30

**Regex in target scope:** match hosts and paths with patterns like `.*ohsome.*` without giving up `*.acme.com` wildcards.

### Added
- **Target scope regex patterns.** Host and path rules accept regex when the pattern contains metacharacters (e.g. `.*ohsome.*` matches `cdn.ohsome.com`) or is wrapped in slashes (`/pattern/`). Leading wildcards (`*.acme.com`) and exact hosts still work as before.

## [0.18.0] - 2026-06-30

**Control where the UI listens:** change the control-plane port from Settings or the command line — no more digging for env vars.

### Added
- **Control UI address in Settings.** Change where the web UI and REST API listen (port or bind address) from **Settings → Proxy & network**, same loopback/external-bind rules as the proxy listener. Persisted as `control.addr`.
- **`--control-port` / `--control-addr` flags.** Set the control UI/API listen address at launch without env vars (e.g. `interceptor --control-port=9967` or `interceptor --control_port=1234`). CLI overrides env and persisted settings for that run.

## [0.17.0] - 2026-06-30

**Usable findings & checks:** attach PoC flows without the proxy-selection dance, jump to any flow by id, and override built-in passive/active checks with editable Starlark.

### Fixed
- **Map tab crash on load.** `renderMap` declared `filtered` twice (endpoint list + filter-active flag); renamed the boolean to `hasFilters`.
- **Findings “＋ PoC flow” always rejected.** The button only read Proxy History multi-select (`state.selected`), which plain-click clears; it now falls back to the inspected flow, opens a flow-picker modal when nothing is ready, and shows a “· N ready” hint when History selection applies.

### Added
- **Proxy History flow-id search.** Search scope **id** (or `#285` / `id:285` in any scope) finds a flow by its numeric id; command palette and the PoC flow picker match ids too.

### Changed
- **Scanner custom checks list readability.** Wider sidebar, multi-line titles, collapsible built-in/active groups (custom sections open first), and a highlight on the check being edited.
- **Editable built-in scanner checks.** Click any built-in passive check or active probe to view/edit its Starlark template; **Save** writes `~/.interceptor/checks/<id>.star` or `active-checks/<id>.star` and replaces the compiled built-in on the next scan. **Revert** removes your override and restores the default built-in.

## [0.16.0] - 2026-06-30

**Map that scales:** large attack surfaces no longer freeze while you search, and ferox/discovery trash collapses into readable clusters instead of a wall of duplicate rows.

### Added
- **Map response clustering.** Endpoints on the same host that share the latest response body (`res_body_hash`, byte-exact) collapse into one row with a **+N identical** badge; click to expand. HTTP **200 soft-404** pages (body matches a "not found" content signature) cluster separately as **soft-404**. Toggle **Collapsing identical** on the Map toolbar (on by default, persisted like **Hiding 403/404-only**). Nothing is auto-deleted — cluster/collapse only.

### Fixed
- **Map search lag on large projects.** Tree rebuild memo no longer keys on the search term; search dims non-matches instead of filtering them out of the tree, so typing stays responsive on 1000+ endpoints. `mapFiltered`, `mapCount`, and `buildGraphTree` are memoized per filter pass.
- **Map `lastStatus` / scheme / body hash pinned to latest flow.** Endpoint aggregation now reads `status`, `scheme`, `res_body_hash`, and `res_len` from the row with `MAX(flows.id)` instead of an arbitrary group member.

## [0.15.0] - 2026-06-30

**Stop what is running:** `interceptor stop` gracefully shuts down every Interceptor instance on the machine (SQLite flush, proxy drain, ports 8080/9966/…), with a 6s grace window before force-kill.

### Added
- **`interceptor stop` subcommand.** Gracefully stops every running Interceptor instance on the machine (flushes SQLite, drains the proxy, frees ports 8080/9966/…), falling back to a force-kill after a 6s grace window. Useful before launching a new version or reclaiming the proxy ports for another app. Reuses the existing signal-driven shutdown path; no daemon or PID file.

## [0.14.0] - 2026-06-30

The **"you can attack that"** release: user-authored active checks, a fully automated agent pentest harness (readiness → discovery → auth → CSRF-aware active scan), Android ADB onboarding, and a unified Checks manager — plus a sweep of UI performance and accessibility fixes for large projects.

### Added
- **User-authored ACTIVE attacks (Starlark-for-active).** You can now write your own *active* checks — not just passive ones. A custom active check is a `def check(point, baseline, probe)` Starlark script where `probe(payload)` sends a real mutated request through the engine (recorded, session-auth applied, counts against the run's budget) and `finding(...)` reports a confirmed vuln. Fully sandboxed (no files/sockets/imports, step-bounded). New `internal/activescript` package; REST CRUD + a **Test** endpoint (`GET/PUT/DELETE /api/active-checks[/{id}]`, `POST /api/active-checks/test`); the Checks manager gains a **CUSTOM · ACTIVE** section with create/edit/delete/test (the AI-Describe tab is passive-only). Custom active checks run alongside the built-in probes when you arm & run an active scan, and are toggleable in the same manager (namespace `custom-active:` so they never collide with built-in IDs).
- **Unified Scanner-checks manager.** The Checks modal now lists *every* module in one place: **18 built-in passive checks** and **9 active-scan probes** (all individually **toggleable** on/off via one shared `checks.disabled` setting), plus your **custom Starlark checks** (full create/edit/delete + AI-generate). Active probes are tagged ⚡ "sends traffic" and fire only when you arm & run an active scan — the *management* UI is unified, but active *execution* stays consent-gated because it sends real attack requests. `GET /api/checks` returns `builtin` + `active` (each with a stable `id`) alongside `checks`/`disabled`; the disabled set is honoured by the passive scan, single-flow analysis, AI context, **and** the active engine.
- **New passive detection: possible SQL injection.** Responses containing a database error string (`SQL syntax`, `ORA-`, `SQLSTATE[…]`, `sqlite3.OperationalError`, `pg_query failed`, "unclosed quotation mark", `System.Data.SqlClient.SqlException`, …) now produce a **High** "Possible SQL injection (DB error in response)" finding — a strong passive SQLi signal (error-message phrasing only, to keep false positives low).
- **New passive detection: internal IP disclosure** — a private/loopback/link-local IP in a response body yields a **Low** topology-leak finding.
- **First-run setup wizard.** A 4-step guide (point at the proxy → download & trust the CA with OS-specific instructions → add a target-scope host → done) shown once on first boot, and reopenable from **Settings → Project & data → Run setup wizard again**. Auto-skips for returning users who already have captured traffic.
- **Scanner → Findings bridge.** Each passive-scan issue group has a **➕ Promote to Finding** action that creates a curated finding (title/severity/detail/fix) with every PoC flow attached, then opens it — the two views of "vulnerabilities" are no longer disconnected silos.
- **Docs & examples for custom active checks.** The user-authored active-checks feature now ships a reference page ([`docs/custom-active-checks.md`](docs/custom-active-checks.md) — the active twin of the passive `docs/custom-checks.md`), two ready-to-copy examples in [`examples/active-checks/`](examples/active-checks/) (error-based SQLi, reflected XSS), and cross-links from the passive docs and the in-app **Docs** tab. A build-tagged test (`go test -tags examples ./examples/active-checks`) compiles every shipped example so docs edits can't silently ship a broken `.star`.
- **WebSocket click-to-replay.** Captured text frames are now click-to-load into the replay box (the most-expected WS affordance that was missing); the replay headline no longer misleads with a raw "HTTP 200".

### Changed
- **Security-header noise collapsed 5 → 1.** Missing CSP, HSTS, X-Content-Type-Options, clickjacking protection, and Referrer-Policy previously each emitted a *separate* finding (a single HTML page could produce five near-duplicates). They now merge into one **"Missing security response headers"** finding that lists which are missing — Medium when CSP or HSTS is among them, otherwise Low.
- **Intruder tab redesigned for clarity.** The attack bar is now grouped by intent — `target · mode · runtime · Start` — with thin separators between groups, instead of an undifferentiated river of 11 controls. **§ Mark** moved into the Request Template header (where you edit), the **History** toggle moved into the Results header (past runs live with results), and the advanced result-processing fields (**flag / extract / process**) plus **presets** folded behind an **Options ▾** disclosure with consistent labels. The primary path is now just: target → mode → Start.
- **Leaner Session/auth surface (progressive disclosure).** The common case is now "paste a token + Save": global session headers stay visible, while per-host overrides, the token macro, and the login macro each collapse behind a disclosure. The three redundant per-block Save buttons are gone — one **Save session** persists all four mechanisms (and toasts macro completeness / enablement).
- **Scanner toolbar splits passive vs offensive.** The safe passive controls (Run scan, Custom checks) sit on the left; **Active scan** and **OOB** move behind a divider labelled **OFFENSIVE** on the right, so the safe/unsafe boundary is visible at a glance.
- **Proxy toolbar de-cluttered.** Dropped the rarely-used **all/https/http** scheme selector (filterable via search/tags instead), removing the leftmost toolbar slot.
- **Intruder preset save uses the themed modal** instead of a blocking browser `prompt()`.
- **Map search is O(N), not O(N²).** "Expand-to-search" now marks matches in a single bottom-up pass instead of re-scanning every subtree at every node, so typing in the Map search no longer freezes large projects.
- **Leaner, clearer toolbars (progressive disclosure).** Discover folds its 8 advanced knobs (extensions, recursion, threads, delay, soft-404, record, tag-API) behind an **Options ▾** disclosure, leaving Base URL + Start + sources as the default surface. The Intruder attack bar collapses 5 named modes into **Sniper / Lists ▾ / Repeat**, with Battering/Pitchfork/Cluster chosen by a sub-select, and **wraps** instead of overflowing at 1280px. The Map Graph view now offers a **show graph anyway** link instead of a flat refusal past the 200-node cap.
- **Map hides forced-browse noise by default.** Endpoints that *only* ever returned **403** or **404** are filtered out (ferox/discovery dead paths). Paths that later return 2xx, 401, 5xx, etc. stay visible. Toggle **Hiding 403/404-only** on the Map toolbar to show everything (`?hideNoise=0` on the API).
- **Findings export report.** Curated findings only by default (passive-scan appendix dropped — use `?issues=1` or MCP `includeIssues=true` to add it back). Format selector on the Findings tab: **Markdown**, **HTML** download, or **PDF** via print dialog.
- **Android Wi‑Fi proxy hint.** When Wi‑Fi mode needs LAN bind, a **Settings → Proxy** button jumps to the proxy listener section (does not change bind for you).
- **Themed dropdowns app-wide.** All native `<select>` controls use the custom in-app menu (filters, Repeater method, Settings, scope/rules tables, Findings, etc.) instead of the OS picker; dynamically added selects are upgraded automatically.
- **Android ADB device picker UI.** Custom themed device menu (no OS-native dropdown) and USB/Wi‑Fi segmented control; shows model name with serial/transport subtitle.
- **Findings detail reads like a report.** The finding detail pane now renders as a narrative document — prose paragraphs for text blocks, blockquote-style PoC callouts for flows (matching the exported Markdown report), hover-revealed edit controls, and a dedicated Impact callout — instead of boxed table-like blocks with FLOW/TEXT labels.
- **Findings text blocks edit in place.** Narrative paragraphs render as markdown at rest; click to edit overlays a same-size field so the layout doesn't jump, then re-renders on blur.
- **Findings Chain view.** The finding detail pane has a **Report | Chain** toggle: Chain renders the ordered body blocks as a vertical attack-step timeline (numbered rail, full narrative text, PoC callouts, Impact tail) instead of a cramped horizontal SVG graph.

### Added (continued)
- **Android ADB setup (Settings → TLS).** When `adb` is on PATH: install CA (user prompt, system/root, or `auto` for emulators), proxy via USB (`adb reverse`) or Wi‑Fi (LAN IP), **Setup all** (proxy + CA), clear proxy with optional system CA removal. MCP: `android_status`, `android_setup`, `android_teardown`. REST: `GET /api/android/status`, `POST /api/android/setup`, `POST /api/android/install-ca`, `POST /api/android/proxy`, `POST /api/android/unproxy` (`internal/android`).
- **Agent pentest automation (P0–P2).** `check_readiness` v2 returns structured JSON blockers (OOB, auth identities, login macro, scope, traffic). `start_discovery` falls back to the built-in wordlist when empty (optional history seeds). New MCP tools: `get_flow_auth`, `promote_flow_to_authz`, `set_login_macro_from_flow`, `set_login_macro`, `test_login_macro`, `get_discovery_wordlist`, `oob_enable`. JWT extract v2 (`internal/auth/jwtextract`) for Bearer/path/JSON/query/cookie tokens; `cross_host_token_replay` accepts `mode:auto|bearer|path`. CSRF-aware active scan (Laravel XSRF bootstrap, default `csrfAware:true`) with per-endpoint circuit breaker (skip after repeated 419/401/403/502). REST: `GET /api/readiness`, `POST /api/authz/from-flow/{id}`.

### Accessibility
- **Keyboard access to previously mouse-only controls.** History tag chips (filter by tag), sortable column headers (Proxy + Map), the Map's tree endpoints / table rows / params rows, and the Scanner custom-check list rows are now focusable and operable via Enter/Space with correct ARIA roles. Repeater and Intruder tab-close "✕" are real `<button>`s (keyboard-closeable). `wireRowKey` now defers to any focused child promoted to `role="button"`, so nested controls no longer double-activate. (The Map's SVG node-link graph remains pointer-driven — Tree/Table/Params are the keyboard-accessible equivalents.)
- **Saved Views → one dropdown.** The Proxy toolbar's apply/save/delete-view triplet is now a single **Views ▾** menu (count badge, apply, save current, delete) — removes a 1280px-overflow trigger.
- **Authz three-mode picker.** The authorization modal replaces its target radios + floating **Cross-host** button with one **Selected flow | All in-scope | Cross-host JWT** segment that retargets Run and reveals only the controls each mode needs.
- **Findings view labels.** **Report → Edit** and **Chain → Timeline** (clearer that the second is read-only). Intruder **Null → Repeat**, the `encode` field label → **process**, and Discover **Hide len → Soft-404 len**.
- **Inspector Render button is HTML-only.** It's hidden for non-HTML responses (where it used to silently fall through to an ugly raw view) and falls back to Pretty. Find-in-page now reports a live match count in the previously-empty status span.
- **AI agent toggle is provider-aware.** "Let AI send requests" disables itself under OpenRouter (it's Anthropic-only) instead of silently no-op'ing mid-run.

### Removed
- **Dead/redundant UI.** `#aiPulse` (redundant with the Activity tab badge), the Activity "All / With intent" segmented toggle (the free-text intent filter covers it), the Proxy **AI** source-filter button (tag-bar filtering covers it), the Intercept **Apply** button (auto-apply + Enter already commit), the duplicate cloudflared-tunnel block in Settings → Scanner (kept in the OOB modal), the duplicate **Export report** button on the Scanner tab (Findings is the single source of truth now), the no-op `setScanSub` legacy shim, and the request-side Raw/Pretty toggle in Repeater (compact-on-send already normalizes).

### Fixed
- **History jank under heavy traffic.** In virtualized mode (≥120 flows) every `flow.new`/`flow.update` event triggered a full window rebuild (`renderRows`) — under a busy proxy that was dozens of synchronous rebuilds per second. Rebuilds are now coalesced via `requestAnimationFrame` (many events per frame → one render). Separately, the method filter `<select>` was rebuilt on every event after scanning all loaded flows; it's now rebuilt only when a genuinely new method appears (tracked incrementally).
- **Intruder results jank on large attacks.** The results list rebuilt every row (potentially thousands) on each 120 ms poll while running. It's now virtualized above 200 rows — only the visible window is rendered, repainted on scroll (same pattern as the Map table and Proxy rows).
- **Map tab lag on large datasets.** Three causes fixed: (1) `buildMapTree` was rebuilt on every render — including 2–3× per search keystroke — so typing in Map search with thousands of endpoints rebuilt the whole in-memory tree repeatedly; it's now memoized on data-version + active filters and reused across renders. (2) The host `<select>` (`All domains…`) was rebuilt with potentially thousands of options on every re-fetch; it's now rebuilt only when the host set changes. (3) On a busy proxy the SSE stream re-fetched + re-rendered the entire map every 900 ms while the tab was open; it now debounces to 2 s and skips the re-fetch entirely when no new flows have arrived.
- **UI audit fixes (data-loss + correctness).** Findings body-save race that could PATCH one finding's blocks onto another after a fast switch (now snapshots blocks at schedule time, and the detail pane skips rebuilding while a text block is mid-edit, so an SSE round-trip can't wipe the focused textarea). PDF export was dead — `window.open(...,'noopener')` returns `null` per spec, so the print path never ran (dropped `noopener`). Intercept could Forward a different item's body than the editor showed after a fast second click (re-checks `heldSel` after the raw fetch). Repeater/Intruder tab races: `renderRepResponse`/`loadRepHistory`/`repLoadSend`/`sendToIntruder` now guard against a tab switch during an `await` so stale data can't land in the wrong tab. Find-in-page in the response inspector no longer corrupts the syntax-highlight markup (marks now match only inside text runs, never inside tags). Rename/status/delete now toast and update optimistically. Plus: non-virtualized History list no longer rebuilds on every scroll tick (rAF-throttled + skipped when fully rendered), the retention "select all" checkbox tracks per-row changes (`indeterminate`), and a dead `'change':'change'` ternary in match-&-replace wiring was removed.
- **Android ADB device list with spaced serials.** `adb devices -l` rows like MuMu's `(no serial number)     device …` are parsed correctly (serial was split on spaces, so Interceptor reported "no authorized device" despite a connected device). Commands use `adb -t transport_id` (or the default device when only one is connected) because adb rejects `-s "(no serial number)"`.
- **Ctrl+R / Ctrl+I no longer fire outside Proxy History.** Flow send shortcuts now require the Proxy History tab (and ignore keypresses while typing in inputs). Fixes Ctrl+R in Settings still sending the previously selected flow to Repeater.
- **Findings list/detail empty after feature update.** Empty findings no longer serialize `blocks`/`flows` as JSON `null` (always `[]`), the list auto-selects the first finding, and row meta shows step count / "needs content" for bare entries. Bounty-project data repaired via `scripts/fix-findings-bounty.ps1` — interleaved `body` blocks, `impact` populated from legacy `fix`, and corrupted finding #6 (SSO redirect) restored.

## [0.13.0] - 2026-06-29

### Changed
- **Findings is now its own top-level tab.** Promoted out of the Scanner tab into a standalone **Findings** menu (the Scanner tab is now passive-issues only) — findings are first-class, not a sub-view. Flow cross-links ("open finding") and the saved-tab restore now target the new tab.
- **Findings define Impact instead of Remediation.** A finding now captures its **security impact** (what an attacker gains / business consequence) via a new `impact` field, replacing the old "Remediation" field on curated findings. Shown in the finding detail pane and rendered as `**Impact:**` in the exported report. Passive scanner-issue remediation is unchanged. Stored in a new additive `impact` SQLite column; exposed on create/update (REST + MCP), with legacy `fix` still accepted for back-compat.
- **MCP records findings description-first.** `create_finding`/`update_finding` now take `impact` (in place of `fix`), and the `initialize` methodology mandates the workflow: write a finding's **description and impact first**, then **always attach the relevant captured flow(s) as PoC** via `add_finding_poc` — every finding should have a description before evidence and at least one PoC flow when one exists.

### Added
- **`cvss` field on findings.** Findings now carry a dedicated `cvss` field (score or vector, e.g. `9.8` or `CVSS:3.1/AV:N/...`) instead of embedding it in the title — stored in a new additive SQLite column, accepted on create/update (REST + MCP), rendered as `**CVSS:**` in the report, and editable in the finding detail pane.
- **`add_finding_poc` position param.** The MCP tool and `POST /api/findings/:id/flows` accept an optional 0-based `position` to insert a PoC flow block at a specific index in the narrative body (omit = append).
- **`list_flows` tag filter.** The MCP `list_flows` tool and `GET /api/flows` accept a `tag` argument to filter flows by tag (e.g. `tag:auth`).
- **`create_finding`/`update_finding` return a UI deep-link.** Their MCP results include `…/#finding-<id>`; navigating to that hash in the web UI activates the Findings tab and selects the finding.
- **Auto-tag auth flows.** Captured flows whose request path looks like an auth endpoint (`/login`, `/register`, `/logout`, `/auth`, `/oauth`, `/token`, `/sso`, `/mfa`, `/2fa`, `/password`, `/reset`, `/verify`, …) are automatically tagged `auth` (segment-exact, false-positive-guarded), surfacing the auth surface for instant `tag:auth` filtering. Best-effort in the capture/sender/proxy persist path; never blocks forwarding.
- **`create_finding`/`update_finding` accept a `body` param over MCP.** Lets an agent set the full interleaved block structure directly (previously only reachable via raw REST).
- **Track `.cursor/mcp.json` in the repo.** The documented Cursor MCP config (Streamable HTTP to `http://127.0.0.1:9966/mcp`) is now checked in so a fresh clone connects Cursor to a running Interceptor with no manual setup.
- **Project `.mcp.json` for Claude Code.** Checks in the Claude Code MCP config (Streamable HTTP to `http://127.0.0.1:9966/mcp`) so Claude Code connects to a running Interceptor — the Claude-Code analogue of `.cursor/mcp.json`.

### Fixed
- **`normalizeFindingSeverity` no longer downgrades "critical".** A severity of `critical` (any case) was silently mapped to `Medium`; it now normalizes to canonical `Critical` (matching the report's severity ranking).
- **Updating a finding's `detail` preserves interleaved bodies.** A `detail`-only update now replaces just the first text block in place, keeping every flow block in its original position — previously it could reorder/append flows and break the interleaved narrative.
- **Project create/switch on Windows.** Switching or creating a project from the web UI did nothing on Windows: the re-exec used `syscall.Exec`, which Windows doesn't implement (it returns "not supported by windows"), so the process never restarted on the new project. The re-exec is now platform-specific — `syscall.Exec` (in-place image swap) on Unix, spawn-a-fresh-process-and-exit on Windows — with a gated `listenRetry` so the spawned child reclaims the proxy/control ports once the old process releases them (a normal start still fails fast on a genuinely taken port). Verified live on Windows: creating a new project re-execs and lands on it.

## [0.12.0] - 2026-06-28

### Added
- **`diff_flows` capability.** New MCP tool `diff_flows` and `GET /api/flows/diff?a=&b=` endpoint compare two captured flows' responses — status change, response-length delta, header add/remove/change, and a bounded line-based body diff. Lets an AI confirm whether a payload actually changed the response (baseline vs exploit). Body comparison is byte-capped like other tools.
- **Four more passive-scan checks (17–20).** Missing `Referrer-Policy` on HTML responses (Low), mixed content on HTTPS pages (`http://` script/style/img/iframe, Medium), potential open redirect via a request parameter reflected in a 3xx `Location` (Medium, off-host only), and directory-listing exposure via the autoindex title pattern (Low) — each conservatively gated with positive/negative tests.
- **Intruder anomaly auto-flagging.** Results are auto-flagged when their status differs from the modal status or their response length deviates from the median (±20% / 50-byte band), plus a grep-minority signal; new `anomaly` field on results and an amber `∿` highlight on outlier rows so interesting responses stand out instantly.
- **Store retention/query test hardening.** Strengthened `internal/store` coverage (68.7% → 74.1%): GC shared-hash body dedup (a body shared by two flows survives while one flow remains), keyset pagination across all non-id sort keys, `QueryFlowsFilter` combinators, finding-body helpers, and `Missing`-flag propagation through host pruning. No bugs found — invariants held.
- **`crlf` active-scan check.** CRLF-injection / HTTP-response-splitting probe (High severity) injecting CR/LF in five encodings (raw, URL-encoded uppercase/lowercase, double-encoded); confirms via the injected header (or `Set-Cookie`) appearing in the *response headers* rather than body reflection, with a baseline guard.
- **Stale PoC evidence flagging.** When a finding's PoC flow is purged from history (prune/GC), the body block and its annotation are preserved and shown as a dimmed "⚠ evidence deleted — re-capture this endpoint" badge (with a per-finding banner) in the UI and a "⚠ PoC flow #N — evidence no longer in history" note in the exported report, instead of a silently empty/broken PoC.
- **Soft-404 auto-calibration in discovery.** Forced-browse fires 3 random-path probes per directory before each wordlist sweep; if they return a consistent fingerprint (status + body length within ~5%/32 bytes) it suppresses wordlist hits that match that baseline — killing soft-404 false positives. Best-effort (falls back to no suppression on error); opt out with `Spec.DisableSoft404Calibration`.
- **Four new passive-scan checks.** CORS-with-credentials (both the `ACAO: *` wildcard and the reflected-Origin variant, High), sensitive token/credential in the request URL (Medium), `Set-Cookie` missing `SameSite` (Low), and authenticated responses that shared proxies may cache (Set-Cookie without `no-store`/`private`, Low). All conservative, with positive/negative tests.
- **Better MCP argument errors.** MCP tool argument-validation now reports the expected type and the offending value (e.g. `flowId must be an integer (got string "abc")`), truncated to 60 chars with secret-named fields masked — so an AI agent can self-correct instead of looping on a bad call.
- **`xxe` active-scan check.** XML request bodies are now enumerated as a `body/_xml` injection point and probed for in-band XML External Entity injection using a safe internal-entity canary (`<!ENTITY xxe "INTERCEPTOR_XXE_CANARY">`) — no external/SYSTEM file-read entities. Flags High severity when the entity resolves in the response, with a baseline false-positive guard. Skips non-XML requests.
- **Discovery auto-tags API endpoints.** Forced-browse hits whose path looks like an API (`/api`, `/graphql`, version segments like `/v1`, `.json`/`.xml`, etc.) are automatically tagged `api`, with a static-asset veto so `.css/.js/.png/…` aren't tagged. Default-on **Tag APIs** toggle in the Discovery bar; tagging is best-effort and never breaks a run.
- **Activity feed intent filter.** The Activity tab gains an **All / 💭 With intent** toggle (show only actions where the AI stated a reason) plus a free-text intent substring filter — client-side over the loaded feed, preserving workflow-separator grouping on the filtered subset.
- **Finding body size cap.** Finding narrative-body writes are capped at 1 MiB total (HTTP 413) and 256 KiB per text block, enforced on both the REST and MCP create/update paths, to prevent runaway-AI or malicious storage bloat.
- **"Send as" context-menu action.** Right-click any flow in Proxy History (or the inspector pane) → **SEND AS** section lists every saved authz identity. Clicking one loads the flow into a new Repeater tab with the selected identity's auth headers (Cookie/Authorization) substituted — one click to replay a captured request as any test role. The identity list is cached from Settings and refreshed whenever identities are saved.
- **Broken account annotation.** Each authz identity now has a **⚠** toggle button in the Authorization modal. Marking an identity broken (e.g., after a lockout) dims its row, adds a warning badge, excludes it from **SEND AS**, and causes it to be **skipped** (not replayed) in `authz_run` and `cross_host_token_replay` — its result row shows "⚠ broken — skipped" instead of live results. Check sessions also skips broken identities. The `broken` flag is persisted with the identity JSON.
- **Findings narrative body.** Each finding now has an interleaved document body instead of separate "Detail / Evidence" text areas and a flat PoC list at the bottom. The body is a free-order sequence of **text blocks** (markdown) and **flow blocks** (clickable PoC request/response badge with an annotation field). Add text with **＋ Text**, attach selected History flows with **＋ Flow**; reorder with ↑/↓; delete with ✕. Content auto-saves on blur. Existing findings (detail + attached flows) are transparently migrated to blocks on first read — no data loss. The export report renders blocks in author order (text paragraphs interleaved with `> GET host/path → STATUS` flow quotes). MCP backward-compat preserved: `update_finding(detail=...)` updates the first text block; `add_finding_poc` appends a flow block; `list_findings` syncs detail from the first text block.
- **Authorization test matrix view.** The authz results area now has a **List / Matrix** toggle for bulk runs. Matrix view shows a single table with endpoints as rows and identities as columns, with per-cell status and size — far easier to scan when testing many endpoints × roles. `⚠ same access` flags highlight the row.
- **Cross-host JWT replay.** New **↔ Cross-host** button in the Authorization modal (and MCP tool `cross_host_token_replay`). Extracts the Bearer token from the selected flow and replays the same path to every unique in-scope host in history — automates detection of cross-environment JWT confusion (shared secret / same-secret multi-tenant bugs). Results show accepted/rejected per host with a link to the captured flow.
- **Per-host session headers.** The Session module now supports host-specific auth overrides alongside the global headers. Set a different Authorization/Cookie per hostname — when a send target matches, the host override replaces the global headers for that request. Exposed via Settings → Session / Auth (UI table with `+ Add host` rows), the `set_session` API (`hostHeaders` field), and the MCP `set_session` tool (`hostHeaders` JSON object). Eliminates the friction of testing multiple targets simultaneously (the main use case: two auth domains, one project).
- **JWT expiry countdown in session UI.** Settings → Session now shows a live expiry timer (`Expires in Xh Ym`) parsed from the Bearer token in the global session headers. Turns amber under 30 minutes and red under 5 minutes. Refreshes every 30 seconds.

### Fixed
- **Intruder grep on compressed responses.** Grep-match and grep-extract now decompress `gzip`/`deflate`/`br`/`zstd` response bodies before matching (previously an encoded body silently matched nothing). Genuinely binary responses (`image/*`, etc.) are skipped and flagged `binary` on the result instead of failing quietly. Decompression logic was consolidated into a shared `internal/codec.DecompressBody` (also used by the response viewer).
- **History live refresh for MCP/tool sends.** Repeater, Intruder, active scan, and discovery sends now broadcast `flow.new` over SSE (via `sender.SetOnPersist`) so Proxy History updates live for AI/MCP traffic — not only proxied browser traffic. Virtualized History (≥120 rows) re-renders on incremental updates instead of patching a single DOM row.

### Added
- **Intruder Battering ram + Cluster bomb.** New attack types matching Burp: `battering` applies one payload list to every § marker at once; `cluster` runs the cartesian product of per-marker lists. UI attack bar, MCP `start_intruder`, and tests.
- **Intruder attack presets.** Save/load attack setups in the Intruder bar via localStorage (`presets…` / 💾).
- **History row virtualization.** Proxy History virtualizes the flow list when ≥120 rows are loaded — only visible rows stay in the DOM.
- **Inspector find-in-response.** `Ctrl+F` on the Proxy tab opens a find bar on the response pane with match highlighting.
- **Response Render tab.** HTML responses preview in a sandboxed iframe (Raw / Pretty / Render).
- **`internal/httplines`.** Shared header normalizer for Repeater/MCP — accepts `"Key: Value"` lines or a JSON object (fixes MCP agents passing `headers` as a map).
- **MCP `flowId` alias.** `analyze_flow`, `get_flow`, and related tools accept `flowId` as well as `id`.
- **MCP Cursor auto-sync.** Project `.cursor/mcp.json` uses Streamable HTTP (`http://127.0.0.1:9966/mcp`) so MCP matches the running Interceptor after restart — no stale stdio subprocess. `scripts/interceptor-mcp` resolves the latest binary for stdio clients; `interceptor update` prints an MCP restart hint.
- **In-scope history pagination fix.** `?inScope=1` pages until enough in-scope rows are found; `GET /api/flows/inscope` for readiness checks.
- **Param miner.** `GET /api/params` aggregates query, form, and JSON keys from captured traffic; Map tab **Params** view lists them per host with send-to-Intruder shortcuts.
- **OOB tunnel helper.** OOB modal and Settings → Scanner show a copy-paste `cloudflared` one-liner for a public callback URL.
- **Ask AI agent mode (opt-in).** The ✨ Ask AI modal has a per-session **Let AI send
  requests** toggle (default off). When enabled with Anthropic as the provider, the
  model can call `send_request` and `get_flow` (up to 5 tool steps per question) to
  probe URLs via Repeater — cookies/auth headers are seeded from the selected flow.
  Tool steps appear as Tool bubbles in the thread; the final answer still streams over
  SSE (`event: tool` during the loop, then `data:` text chunks).
- **Ask AI follow-up questions.** The ✨ Ask AI modal now keeps a conversation thread:
  ask a question, read the streamed answer, then ask follow-ups in the same panel
  (prior Q&A is sent as `history` so the model stays in context). The thread renders
  as You / AI bubbles; **↺ New chat** clears it; **Copy** grabs the whole exchange.
- **Tag removal in History.** Right-click a flow (TAGS section) or a tag chip to
  remove one tag from that flow — or from every row in a multi-selection. Bulk
  `POST /api/flows/tags` now accepts `"remove": [...]` alongside `"add"`. MCP:
  `untag_flow`.
- **Active scan request log.** Every probe is recorded as a `FlagActiveScan` flow
  (including transport errors as 502) whether or not it confirms a finding.
  `GET /api/activescan` includes a live `logs` array for the current run;
  `GET /api/activescan/history` lists all saved probes. The ⚡ Active scan modal
  shows a **Request log** panel (click a row to inspect).
- **Intruder file payloads — preview only in UI.** Loading a wordlist with 📂/＋
  keeps the full list in memory for the attack but shows only the first 40 lines
  in the editor (readonly). Counts and Start still use the full list; huge lists
  are not written to localStorage.

### Changed
- **Engagement report quality.** `internal/report` now orders findings by an explicit severity rank (Critical→High→Medium→Low→Info), adds an executive-summary table (counts by severity and by status), and moves `false_positive` findings out of the main body into a dedicated "Excluded — False Positives" section. Deterministic ordering; PoC rendering and the passive-scan appendix unchanged.
- **Session injection is scope-gated by URL path** (not just host) — Repeater/Intruder sends only attach auth headers when the target URL matches scope rules; `session.unscoped` opt-in still sends everywhere (unsafe).
- **System font stack** replaces Google Fonts JetBrains Mono — works offline/air-gapped.
- **Windows proxy onboarding** in the get-started card (`Settings → Network → Proxy` / `netsh winhttp`).
- **Scope duplicate warning** in Settings → Target scope when identical rules exist.
- **History source filters: Manual + AI toggles.** Replaced the confusing **🤖 AI** /
  **🤖 only** pair with independent **Manual** and **AI** buttons (both on by default).
  Enable both to see proxy + bot traffic; disable one to see only manual captures or
  only AI sends. API: `?manual=0|1&ai=0|1` (`?onlyAi=1` still works for AI-only).
- **History sort is server-side.** Column headers send `?sort=&dir=` to the API
  (keyset pagination via `curId`/`curVal`, legacy `before=` for id DESC). Sorting
  no longer reorders only the loaded browser page — id ascending starts at flow #1
  and infinite scroll fetches the rest. Initial fetch loads 250 rows plus a 50-row
  buffer to reduce scroll lag.
- **History uses infinite scroll + virtualization.** Loads older flows on scroll;
  lists ≥120 rows virtualize so the DOM stays bounded.
- **AI assist is now a single "Ask AI" question box** instead of the Explain /
  Payloads / Summary preset modes. Open it on a flow (or a multi-selection) and ask
  anything about the captured request/response — "is the CSRF token validated?",
  "what auth scheme is this?", "suggest test payloads" — and the answer streams in,
  grounded in the exchange. Simpler and more flexible (one box does what the three
  presets did). Backed by `kind:"ask"` + `question` on the assist endpoints; the
  preset segmented control and the structured-payload cards were removed.
- **Scanner tab absorbs Findings.** Passive scan issues and curated findings share
  one tab with a **Passive / Findings** toggle — fewer top-level tabs.
- **API & MCP moved to Settings.** API keys, REST reference, and MCP config live
  under Settings → **API & MCP**; the standalone API tab is removed.
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
  **⌘M** shortcut that jumped to the Map tab filtered to the selected flow. **Search
  in Map (body)** (inspector text selection) and the Map tab itself are unchanged.
- **History "Export" / "Import" (HAR) toolbar buttons.** Removed from the Proxy
  History toolbar (unused in practice). The `/api/export/har` and `/api/import/har`
  endpoints are unchanged, and full project export/import remains in Settings.
- **History "🔎 discover" filter button.** It only showed content-discovery hits,
  which are already marked with a "DSC" badge on their rows and findable via the
  Discover tab — so the toolbar toggle was redundant. The `?discovery=1` API filter
  on `/api/flows` is unchanged.

