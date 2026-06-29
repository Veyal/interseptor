# Changelog

All notable changes to **Interceptor** are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

## [0.11.0] - 2026-06-27

### Added
- **Flow tags — Map filter by tag.** The Map tab has a tag selector that restricts
  the endpoint map to endpoints with at least one flow carrying that tag, so you can
  see just the attack surface you've triaged under a tag. Backed by `?tag=` on
  `/api/endpoints` (`EndpointFilter.Tag`, tested).
- **Flow tags — quick-bar + per-tag colors.** A tag strip above History shows every
  tag in use (with flow counts) as one-click filter chips; the active filter is
  highlighted. Right-click a tag chip to assign a color from a preset palette (or
  clear it), and that color is applied to the chip and to the tag's chips on history
  rows. Colors persist (per-tag) and update live across clients over SSE.
- **Flow tags — manual + AI tagging, chips, and filtering (usable end-to-end).**
  Right-click a History flow → **TAGS** to add tags (prefilled with the flow's
  current tags, so removing one is just deleting it), or tag a whole multi-selection
  at once. Tags render as clickable chips on history rows — click a chip (or the
  right-click entry) to filter History to that tag; it shows as a removable filter
  chip. The AI can tag flows over MCP (`tag_flow`) and reuse existing tags
  (`list_tags`). Tag edits appear live via SSE.
- **Flow tags — REST API + filter.** New endpoints: `PUT /api/flows/{id}/tags`
  (replace a flow's tags), `POST /api/flows/tags` (add tags to a selection of
  flows), `GET /api/tags` (tags in use with counts and colors), and
  `PUT /api/tags/{tag}/color` (hex-validated). History/Map can filter by
  `?tag=`, flow rows and the flow detail now carry their `tags`, and tag changes
  broadcast over SSE so clients update live. Tested end-to-end.
- **Flow tags — storage foundation.** Flows can carry short labels (tags) for
  triage, filtering and Map grouping. Backed by new `flow_tags` and `tag_meta`
  tables (kept off the hot insert/scan path), with normalization (lowercased
  slugs, deduped, capped), set/add/remove, batch-load for list pages (no N+1),
  distinct-tags-with-counts-and-colors, per-tag colors, and a `Tag` history
  filter. Tags are removed when their flow is purged. (API/MCP/UI in follow-ups.)
- **Login macro: a "Test" button (dry-run).** Settings → Session → Login macro now
  has a ⚗ Test button that runs the recorded login request and shows the response
  status and exactly which session headers (Cookie / Authorization) it captures —
  **without** applying them to your live session, so you can debug a macro before
  relying on it. If it captures nothing, it says so and why. Backed by the new
  dry-run `POST /api/session/login/test` and `sender.TestLoginMacro`.
- **Findings can be renamed, and creating one from History prompts for a name.**
  The finding detail header has a ✎ rename button (edits the title via the existing
  PATCH). Creating a finding from a Proxy-History selection ("➕ Add to finding" →
  "＋ New finding from these flows") now asks for a title up front instead of
  silently creating one called "New finding".

### Changed
- **History multi-select: Ctrl/Cmd-click toggles individual rows, and rows no longer
  select text.** Ctrl/Cmd-click now adds/removes a single (non-contiguous) row from
  the selection — previously this required the awkward Ctrl+Shift+click. Shift-click
  still selects a contiguous range. The history rows are also marked
  `user-select:none`, so shift/ctrl-clicking to build a selection no longer
  highlights row text (the long-standing annoyance); the inspector panes stay fully
  selectable for copying.
- **UI: the passive-scan report download uses the native Save dialog and honest
  feedback**, like the Findings engagement-report export. It previously triggered a
  hidden-anchor download and toasted "report downloaded" before the download could
  fail; it now fetches via `api()` + `saveFile()` (offering a Save-As where
  supported), surfaces real errors, and says "Downloading…". This was the last
  download path still bypassing the shared `saveFile()` helper.

### Fixed
- **HAR export/import no longer corrupts binary bodies.** `harx` wrote response and
  request bodies into the HAR `text` field verbatim, so `json.Marshal` replaced any
  invalid-UTF-8 bytes (images, gzip, protobuf, any binary) with the U+FFFD
  replacement character — silently corrupting the body on export. Binary bodies are
  now base64-encoded with `encoding:"base64"` (HAR 1.2), and `Parse` decodes
  `encoding:"base64"` content — so HARs from Chrome/Burp/Firefox (which base64 their
  binary bodies) now import correctly too instead of storing the literal base64
  string. Text bodies are unchanged. Covered by a binary round-trip test.
- **UI: the token-macro "saved" toast no longer overclaims.** Saving the token
  macro with the toggle on but required fields blank reported "token macro on —
  fires before each send", even though the backend only fires it when target,
  request, extract and inject-name are all set (so it silently did nothing). The
  toast now says what's needed for it to actually fire.
- **UI: destructive actions that lose work now ask for confirmation.** Revoking an
  API key (which immediately breaks any client using it and can't be undone) and
  deleting a custom Starlark check (which discards the hand-written source) both
  fired on a single click; they now show the same themed danger-confirm dialog the
  rest of the app uses for destructive operations, naming the key/check involved.
- **UI: user-triggered actions that failed silently now give feedback.** The custom-
  checks list left a blank panel when its load failed (now shows an inline error);
  the OOB "Clear" button and the active-scan "Stop" action swallowed errors with no
  toast (now confirm success / surface the error), matching the Discover "Stop" and
  OOB generate/save siblings.

## [0.10.0] - 2026-06-27

### Added
- **Findings ↔ flow cross-linking + History "AI-only" filter.** A flow's right-click
  menu shows the findings it's a PoC for ("📌 In finding") and an "➕ Add to finding"
  action, so you can navigate both directions (and record any flow as evidence in one
  click). A new "🤖 only" History toggle (`/api/flows?onlyAi=1`) shows just the flows
  the AI sent, so you can watch exactly what it did.
- **AI↔human handoff.** The AI can pause and ask the operator before a high-impact
  or ambiguous action via the `request_human_input` MCP tool ("found IDOR — fuzz
  ids 1-100?", with optional suggested answers). The question appears in a banner
  at the top of the UI; the operator answers (or picks an option) and the AI gets
  the reply — inline if answered within ~40s, otherwise via `get_human_response`
  (poll). Pending prompts survive an SSE reconnect. So the AI never exceeds the
  human's authority on consequential steps.
- **Activity feed shows the AI's intent.** Consequential MCP tools accept an
  optional `intent` (a short "why"), recorded and shown beneath the action in the
  Activity feed — so the human sees the reasoning, not just the request.
- **AI setup tools + pentest methodology (MCP).** New `check_readiness` (pre-flight
  checklist: proxy listening, CA-for-HTTPS hint, scope set, in-scope traffic
  captured) and `scope_from_url` (self-scope from a target URL → include rule for
  its host/scheme) let an AI get set up and self-scope. The MCP `initialize`
  instructions are now a real methodology (setup → recon → authz/IDOR → injection →
  verify → **record findings via `create_finding`/`add_finding_poc` as it goes**),
  so the AI tests systematically and leaves a curated trail the human can take over.
- **Findings: a curated, persistent vulnerability store with PoC evidence.** A
  project can hold many findings (title, severity, status, detail, evidence, fix),
  each with **multiple request/response flows attached as proof-of-concept** —
  selected from captured History. Distinct from the ephemeral passive-scanner
  issues: findings carry an operator-managed status (`open` → `verified` /
  `false_positive` / `wont_fix` / `fixed`) and a source (`human`/`ai`/`scanner`).
  Backed by `findings` + `finding_flows` tables, a REST+SSE API
  (`/api/findings`, `/api/findings/{id}/flows`), and MCP tools (`create_finding`,
  `list_findings`, `update_finding`, `add_finding_poc`, `remove_finding_poc`) so
  the AI records findings as durable, structured memory the human reviews. A
  **Findings tab** lets the human review/curate findings, change status, read the
  markdown write-up and each PoC's request/response (click to open), and **attach
  request/responses selected in History** as PoC evidence (selection-bar "➕ Add to
  finding" or the finding's "Add selected" button).
- **Export engagement report.** A new "⤓ Export report" button in the Findings tab
  (and the `export_report` MCP tool, `GET /api/findings/report`) renders the full
  writeup as Markdown: every curated finding (severity, status, detail, evidence,
  remediation, and its attached PoC request/response flows) grouped by severity,
  followed by an appendix of the passive-scan issues. This is the shared artifact
  the human exports or the AI hands off.
- **Activity feed groups an AI's workflows.** Consecutive tool calls that share a
  stated `intent` (or fall within a short time window) render as one block, with a
  separator where one workflow ends and the next begins — so a multi-step sequence
  reads as a unit rather than a flat list.
- **Per-OS CA-trust guidance (Linux + CLI).** The Settings → TLS panel and the
  `ca_info` MCP tool now spell out trusting the CA on Linux (`update-ca-certificates`
  / `update-ca-trust`) and for curl/tools (`--cacert`, `SSL_CERT_FILE`), alongside
  the existing macOS/Windows/Firefox/iOS/Android steps. Trusting the CA stays a
  deliberate one-time manual step — Interceptor never edits the OS trust store.
- **`docs/AUDIT-BACKLOG.md`** — tracked list of audit findings, now fully burned
  down: every item is FIXED or DEFERRED-with-rationale (output of the
  multi-iteration security/quality audit + the backlog-clearing campaign).

### Changed
### Fixed
- **Security: Repeater / Intruder / WS-repeater refuse to target Interceptor's own
  listeners.** A send aimed at the control plane (`127.0.0.1:9966`) or the proxy
  port was an SSRF / self-pivot the AI/MCP agent could be coerced into (e.g.
  reading `/api/keys`); such targets are now rejected with 403. Blanket internal-IP
  blocking is intentionally avoided — pentesters legitimately target internal hosts.
- **Security: match/replace and intercept-filter regexes are length-capped (4 KB).**
  An over-long pattern runs against large bodies on every proxied request; real
  patterns are short, so over-cap patterns are rejected at the API.
- **History API: bad `limit` no longer panics.** `GET /api/flows?limit=-1` (any
  non-positive or absurd value) previously hit `flows[:limit]` and panicked
  ("slice bounds out of range", recovered as a 500); bad limits now fall back to
  the default 200.
- **Body store: concurrent identical captures on Windows.** Two simultaneous
  captures of the same body content could make the loser of the create race fail
  `os.Rename` (Windows rejects renaming onto an existing file), spuriously
  reporting a capture error for a body that is in fact on disk. `Finalize` now
  treats an already-present destination as a successful dedup.
- **Security: stored XSS via notebook markdown links.** `renderMD()` interpolated
  a link URL into the `href` attribute without escaping `"`, so an AI/MCP-written
  note link could break out of the attribute and execute script in the control
  origin. The href is now quote-escaped (matching the existing image rule).
- **Security: stored XSS via notebook image MIME.** Notebook images were served
  with a caller-controlled `Content-Type` and no `nosniff`, so an image stored as
  `text/html`/`image/svg+xml` could execute as active content. MIME is now coerced
  to a raster-image allowlist on both insert and serve, and `getNotesImage` sends
  `X-Content-Type-Options: nosniff`.
- **Security: curl export quotes the request method.** `curlgen` now single-quotes
  the `-X <method>` value like every other field, so a method with shell
  metacharacters can't inject when the generated command is pasted into a shell.
- **UI: Compare-responses modal closes on Escape / backdrop click** like every
  other modal (it was missing from `MODAL_IDS`).
- **UI: intercept Forward shortcut cheatsheet** now shows `Ctrl+Shift+F` (the
  actual binding) instead of `Ctrl+F`.
- **UI: Repeater send errors no longer leave the status stuck on "sending…"** —
  the catch path now clears the placeholder and shows the error in the response pane.
- **UI: Ctrl+D no longer drops the held request while you're editing it** — the
  intercept drop shortcut is suppressed when focus is in an input/textarea.
- **UI: Discover Start can't be double-submitted** — the button is disabled
  synchronously on click until the server reports the scan running.
- **Security: stored XSS via host name in the retention delete dialog.** The
  per-host "Delete flows" confirm built its message from a raw `host` rendered
  via `innerHTML`; a proxied host containing markup could execute in the control
  origin. The host is now escaped (the dialog title already was).
- **UI: Intruder marker tooltip attribute escaping.** A `§` marker's captured
  text was placed in a `title="…"` attribute via `esc()` (which doesn't escape
  quotes), so a marker containing `"` broke the attribute. Now uses `escAttr()`.
- **UI: Active-scan findings render no longer crashes on a finding without a
  `point`.** `renderActive()` dereferenced `f.point.kind` unguarded, throwing on
  every `activescan.update` event for such a finding; the field is now optional.
- **UI: flow selection race.** `selectFlow()` could overwrite the note field,
  status line and `state.detail` with a slower earlier request's data after you'd
  already moved to another flow; it now bails if the selection changed mid-fetch.
- **API: security-guard errors are now JSON.** `securityGuard` rejections (rejected
  Host / cross-origin / missing MCP key) returned plain text, so the UI's `api()`
  wrapper showed a generic "Forbidden" instead of the explanatory message. They now
  use the same `{"error":…}` JSON shape as every other handler.

- **Control API: malformed JSON bodies are rejected instead of silently flipping
  state.** Ten handlers ignored their `json.Decode` error, so a malformed body
  decoded to a zero value and silently acted on it — e.g. disarming the active
  scanner, turning intercept off, or disabling the OS system proxy. They now
  return 400 on malformed JSON while still tolerating an empty body (`io.EOF` →
  zero value), so existing empty-body callers (e.g. forward-unchanged) are
  unaffected. Covers the intercept toggle/filter/forward handlers, active-scan
  arm/start, sysproxy, project switch, and API-key create.
- **Bulk delete/purge reject absurdly large id/host arrays.** A request whose
  `ids`/`hosts` array fit within the body cap but held tens of millions of entries
  amplified ~10× into a multi-hundred-MB allocation (`make([]any, len)` + SQL
  placeholder string); both endpoints now reject arrays over 100,000 entries.
- **Control API: every request body is bounded (128 MiB) as a DoS backstop.** ~40
  handlers decoded JSON request bodies with no size limit, so a single huge body
  (loopback- or AI/MCP-reachable) could exhaust memory. A `http.MaxBytesReader`
  cap is now applied to all control routes in the security middleware; the tighter
  per-endpoint limits (checks 512 KB, HAR 64 MiB, OOB 512 B) still apply on top.
- **Intruder: a runaway attack spec can no longer OOM the process.** `buildJobs`
  truncated to the request cap only *after* materializing every job, so a huge
  `repeat` count (or a template with thousands of §markers × payloads) allocated
  billions of jobs first. The cap is now enforced *during* accumulation.
- **Custom checks: the Starlark source is size-capped (512 KB) before parsing.**
  The save/test endpoints decoded an unbounded request body and handed it straight
  to the lexer; a multi-hundred-MB body could exhaust memory/CPU on the control
  goroutine. The body is now bounded with `io.LimitReader`.
- **OOB token fallback is unique even if `crypto/rand` fails.** The (essentially
  unreachable) fallback used a wall-clock-derived token that could collide or be
  guessed; it now uses a monotonic counter.
- **Repeater/Intruder tabs survive a corrupted `localStorage` tab list.** A persisted
  tab missing its `tid` made the next-id seed `NaN`, which broke tab selection; the
  seed now ignores non-finite ids.
- **Discover "Stop" gives feedback.** It silently swallowed errors; it now toasts on
  success or failure.
- **Accessibility: the right-click context menu exposes `role="menu"`/`menuitem`** so
  screen readers announce it as a menu (it already had full keyboard navigation).
- **Map/endpoints cache is refreshed after importing a HAR or project.** Both
  import handlers inserted flows and nudged the UI but never invalidated the
  endpoints aggregate cache, so the Map tab showed the pre-import list until the
  next live capture. They now invalidate it like the other mutation sites.
- **TLS: expired per-host leaf certificates are re-minted instead of served from
  cache.** `LeafForHost` returned a cached leaf without checking its validity, so a
  proxy process running past the leaf window (or one minted against a near-expiry
  CA) would serve an expired cert and TLS clients would reject the MITM. The cache
  hit now falls through and re-mints when the cached leaf is at/near expiry.
- **UI: API-keys list shows "—" instead of "Invalid Date"** when a key has no
  parseable `created` timestamp; and the Map tab's search/method/refresh controls
  are now null-guarded at import like their siblings (defensive, no behavior change).
- **Active scan: cmd-injection timing check no longer false-confirms on an
  un-run control.** If the request budget was exhausted at the `sleep 0` control
  probe, it returned duration 0 (`< 3s`) and the check reported a confirmed
  finding; the control must now actually run (`Status != 0`) to confirm.
- **HAR export: flows with an unknown scheme produce a valid URL.** A flow that
  errored before its scheme was determined exported as `://host/path`; `flowURL`
  now defaults the scheme to `http`.
- **Intruder: repeat-mode jobs no longer alias one payload slice** (defensive —
  each repeat job now gets its own copy, matching the sniper branch).
- **Store: `OpenBody` no longer panics (or escapes the bodies dir) on a malformed
  hash.** A body hash shorter than 4 chars (e.g. from a crafted HAR import) panicked
  `bodyPath`'s `sum[:2]`/`sum[2:4]` slicing, and a traversal string like `../../x`
  could read outside `bodiesDir`. `OpenBody` now validates the hash is a 64-char
  lowercase sha256 hex and returns `os.ErrNotExist` otherwise.

- **Accessibility: screen-reader labels for icon-only controls and key inputs.**
  The seven modal close buttons that were bare "✕" (flow, checks, active-scan, OOB,
  authz, compare, decode) now have `aria-label="Close"`; the History search, Repeater
  URL and Map search inputs (previously placeholder-only) have `aria-label`s; and the
  intercept status bar is now an `aria-live="polite"` region so a held-request count
  is announced. (Pure additive — no behavior change.)

- **Accessibility: clickable rows are keyboard-operable.** The `<div>` rows wired
  with `onclick` — History flow rows, the held-intercept queue, scanner scan-items
  and active-scan findings, Repeater/Intruder tabs and history, finding and PoC
  rows, and authz result rows — were unreachable without a mouse. A shared
  `wireRowKey()` helper now marks them `role="button"` + `tabindex="0"` and activates
  on Enter/Space (live-verified: Tab focuses a flow row, Enter selects it).
- **Accessibility: segmented toggles announce their pressed state.** Segmented
  `<button>` groups across the AI modal, API sub-nav, flow modal, notes, Map,
  Repeater/Intruder and Settings nav now set `aria-pressed` in sync with the visual
  `.on` class; the remaining placeholder-only inputs (match/replace, scope, intruder
  grep/extract/processing, token/login macros, OOB, check-id, scan/authz limits,
  API-key label, note) gained `aria-label`s; and the dark-theme muted text colour
  `--fg3` was nudged `#8e8e99`→`#9a9aa6` for ~AA contrast on cards.
- **Proxy (forwarding path): body-transforming intercepts are bounded and never
  truncate.** Response/request interception and body match-replace read the whole
  body with `io.ReadAll` (breaking the stream-to-disk invariant) and could forward a
  silently-truncated body on a read error. They now use `io.LimitReader(64 MiB)` and
  forward the body untransformed (via a `restoreBody` `MultiReader` that preserves
  `Close`) when it exceeds the cap or errors. Live-verified.
- **Proxy (forwarding path): chunked HTTP/1.1 responses keep the connection alive.**
  `keepAlive` was false for chunked responses (ContentLength −1), tearing down the
  MITM tunnel (and forcing a TLS re-handshake) after every such response.
- **Proxy: WebSocket upgrades honour the chained upstream proxy.** WS dials went
  direct even when an upstream proxy was configured; they now CONNECT-tunnel through
  it (the direct path is byte-identical when no upstream is set).
- **Proxy: plain-HTTP responses forward their trailers.** `writeResponseHTTP`
  dropped `resp.Trailer` (the MITM path already preserved them).
- **Store: `InsertFlow` indexes the FTS row in the same transaction** as the flow
  insert, so a crash between the two can't leave a flow invisible to search.
- **Store: `UpdateFlow` drops the per-response pre-SELECT** — it now updates the row
  and the `flows_fts` columns directly in one transaction (less work on the hot
  proxied-response path).
- **Store: `ws_frames` is capped per flow** (most-recent 5000), so a long-lived
  WebSocket can't grow the table unbounded.
- **Store: bounded TLS leaf-certificate cache.** `LeafForHost` evicts FIFO past
  2048 hosts, so subdomain fuzzing can't grow the cache without limit.
- **Retention: body GC no longer blocks the purge response.** `GCBodies` (a full
  bodies-dir walk) runs in the background after the purge is acknowledged.
- **Security: `re_search` custom-check builtin caches compiled regexes and caps
  input** (256 KB), closing a CPU sink (the Starlark step counter doesn't tick
  during `regexp`).
- **Security: MCP authorization fails closed.** A `HasAPIKeys` store error no longer
  flips `mcpAuthorized` open once any key is known to exist.
- **Sender: the login macro no longer thunders.** Concurrent sends hitting an
  expired refresh TTL all fired the macro; an in-progress sentinel now lets exactly
  one run.
- **wsrepeater: the connection deadline is reset after the handshake**, so a slow
  TLS handshake can't consume the frame-write budget and cause a silent timeout.
- **Discovery: data race on the budget-exhaustion path removed** — a local
  `budgetHit` flag replaces reassigning the shared `ctx` while workers read it.
  (Final `go test -race` confirmation pending a cgo-enabled host.)
- **Active scan: boolean-SQLi check skips tiny baselines** (`< 64 B`) whose length
  thresholds truncated to zero and could false-positive.
- **UI: SSE `ws.frame` debounce no longer double-fires** — `WSFramed` compares the
  stored timer by identity so a stale `AfterFunc` can't delete the new entry.
- **UI: Map table sort routes through `renderMap()`** so the breadcrumb, count and
  warning stay current after sorting.
- **UI: the flow inspector won't render the wrong flow.** `fmRenderSide` snapshots
  the flow id and bails if a newer `flowPopup` superseded it mid-fetch.
- **UI: authz tests the currently-selected flow.** The target is resolved at action
  time (`state.selId` wins) across "from flow", "check sessions" and "run", so
  changing the History selection behind the open modal no longer tests a stale flow.
- **UI: export-project / download-CA toasts** say "Downloading…" instead of claiming
  success before the download is confirmed.

### Changed
- **Store: bulk flow deletes are now atomic and avoid an N+1 query.** `DeleteFlows`
  ran one `SELECT` per id to fetch columns that the FTS unindex never used, then
  deleted the FTS rows and the flows in separate non-transactional statements. Both
  `DeleteFlows` and `DeleteFlowsByHost` now batch-delete the FTS rows by rowid and
  wrap the FTS + flows deletes in a single transaction — so a "select all → delete"
  of N rows is 2 statements instead of ~2N, and the full-text index can never be
  left out of sync with the flows table.

### Removed
- **UI: dead `state.selHeld` / `state.selRespHeld` keys** (never read; the live
  key is `state.heldSel`) — removed to prevent a future refactor footgun.

## [0.9.0] — 2026-06-26

### Added
- **History: body search.** Toolbar scope selector **path** vs **body** — body mode scans
  request/response content (bounded; shows a note when truncated).
- **History: notes filter.** **📝 notes** toggles flows-with-notes only (`hasNote=1`).
- **History: response comparer.** Select exactly two flows → **⇄ Compare** in the
  selection bar — line diff of response bodies.
- **Custom checks: per-check enable.** Checkbox in the checks list; disabled ids persist
  in settings and are skipped on passive scan.
- **MCP: authz + OOB tools.** `get_authz`, `set_authz`, `authz_run`, `authz_check_sessions`,
  `oob_state`, `oob_new`, `oob_set_base`.
- **Project export: notebook + authz identities.** Portable bundle includes notes markdown
  and `authz.identities`; import restores them.
- **Discover: inspect found paths.** Click a result row to open the request/response
  popup (same as Map/Scanner). **⧉ Copy URL** lives in that modal header. When
  **Record hits in History** is on, each hit gets a `flowId` as it is found; otherwise
  inspect re-sends the URL on demand via `POST /api/discovery/inspect`.
- **AI organize project notes.** Notes tab **✨ Organize** sends your notebook to your
  configured AI provider and streams a structured draft (Scope, Credentials, Findings,
  To-do) in a side-by-side preview; **Apply** replaces the notebook. Also in the command
  palette when AI is enabled.
- **Disable all AI features (Settings → AI assist).** One checkbox turns off BYO-key
  assist, the Activity tab, AI paths in Discover, History AI filter/buttons, and
  context-menu AI actions. AI HTTP endpoints return 403; MCP and non-AI control APIs
  stay available. Re-enable anytime from the same screen.
- **Custom checks: Docs + AI describe.** The checks modal has a **Docs** tab
  (embedded check authoring reference — flow API, builtins, examples) and a **✨ Describe**
  tab (when AI is enabled): plain-text → `POST /api/ai/checks/generate` → Starlark source,
  suggested id, and automatic **Test** against the selected flow.
- **Custom checks shared across projects.** Scanner checks are stored in
  `~/.interceptor/checks/` (global), not per-project. On startup, any checks found under
  old project folders are merged into the global directory without overwriting existing files.
- **Load list files in Discover & Intruder.** Discover wordlists and Intruder payload
  lists have **Load file** / **Append file** buttons (native file picker, up to 16 MB).
  SecLists-style `.txt` wordlists and one-per-line payload files work out of the box.
- **Map search scopes.** Map search has **Path / host**, **Headers**, **Body**, and **All**
  modes. Path/host filters client-side (instant); headers/body/all query the server — body
  search is bounded (content-deduped, latest 8000 flows, 256 KiB per body) so it stays
  practical on large projects.
- **Cross-feature navigation & file loads.** History context menu: **Show in Map**, **Scan this
  host**; inspector selection → **Search in Map (body)**. **Ctrl+M** jumps to Map table view.
  Decoder **Load file**, Intruder **Load** raw HTTP template. Map auto-refreshes on new traffic.
  Command palette: custom checks, active scan, OOB (when enabled), Scanner settings. Discover
  remembers wordlist panel open/closed.
- **Active scan: in-scope preview.** Choosing **all in-scope** in the active-scan
  modal lists your include/exclude rules (from Settings → Target scope), captured
  in-scope hosts, and a link to edit scope — bulk scan still requires at least one
  include rule.
- **Authz: bulk in-scope + session helpers.** Authorization test supports
  **selected flow** or **all in-scope** (up to 30 endpoints, scope include required).
  **⧉ From flow** fills Cookie/Authorization from the capture; **Check sessions**
  probes 401/403 per identity; blank headers now truly mean anonymous (auth stripped).
  Set-Cookie expiry hints shown when opening from a flow.

### Fixed
- **UI failed to load (`Unexpected token ':'` in `core.js`).** A malformed ternary in
  `renderMD` (code-block `data-lang` attribute) broke ES module parsing and blanked the
  whole app until fixed.

### Changed
- **Inspector: auto-decode on selection.** Highlight base64, URL-encoded, hex, JWT,
  or other encoded text in the request/response panes (and the flow popup) — a slim
  amber strip appears under the pane header with the decoded value. Hover to expand;
  ⧉ copy or open the full text in Decoder. Right-click still works too.
- **Proxy History: noted flows highlighted.** Rows with a flow note get an amber
  background and left border instead of a small 📝 icon; hover the row to read
  the note in the tooltip.
- **Command palette: Proxy History label.** The History tab entry now read **Proxy History**
  so Ctrl+K search for "proxy history" finds it easily.
- **Authz: stronger diff + bulk polish.** Response body hash (SHA-256 prefix) improves
  same-access detection; bulk runs skip static assets; anonymous identities strip all
  auth headers (Cookie, Authorization, X-API-Key, …); scope panel lists captured in-scope hosts.
- **Inspector decode in Pretty mode.** Selection maps back to raw source text so tokens
  split across highlight spans still decode correctly.
- **MCP when AI disabled.** `/mcp` and `GET /api/mcp` stay available — only BYO-key AI
  endpoints return 403; agents can still drive flows, authz, OOB, etc.
- **API reference expanded.** `/api/reference` now documents notes, endpoints, OOB, authz,
  activity, project switch, and body search params.
- **Settings: system proxy on Windows.** macOS-only auto-config section is hidden on
  unsupported platforms (manual proxy hint remains in the Proxy section).
- **Custom checks modal height.** The checks modal opens taller
  (`88vh` / 820px) with a proper flex layout so the Starlark source area fills
  the dialog instead of collapsing.
- **OOB catcher disabled by default.** Blind callbacks need a URL the target can
  reach — `127.0.0.1:9966/oob` is useless for remote apps. The **⚲ OOB** button
  is hidden until you enable it in **Settings → Scanner** and set a reachable
  base URL (ngrok, VPS, LAN IP). `/oob/` returns 404 while disabled.
- **Notes AI organize: rich Markdown.** The organize prompt now asks for credential
  tables, inline `` `code` `` for passwords/tokens, fenced blocks for JWTs/.env,
  finding subsections with blockquotes, and `- [ ]` task checkboxes. The notes
  preview renderer supports tables, blockquotes, ordered/task lists, italics, and
  language-tagged code blocks.
- **Repeater / Intruder tab selection.** Active tabs use a background fill, accent
  text, and a bottom underline — no accent border that could clip or misalign with
  neighbours. History sidebar selection uses the same fill + accent label (no left
  border stripe).
- **Proxy History: configurable columns.** **▦ Cols** in the toolbar opens a
  checklist of history columns (#, Method, Host, Path, St, Type, Size, Time).
  Choices persist in localStorage. **Type** is hidden by default.
- **Body downloads use MIME extensions.** Oversized or binary request/response
  bodies download via `GET /api/flows/{id}/body` (body bytes only, not the HTTP
  envelope) as `flow-{id}-{side}.{ext}` — e.g. `.json`, `.html`, `.png`, `.pdf`.
- **Pretty view syntax highlighting.** JSON bodies were already colorized; Pretty
  mode now also highlights **HTML/XML** (tags, attributes, comments) and **CSS**
  (selectors, properties, values), using Content-Type plus a quick body sniff.
- **Flow inspect modal: Copy URL.** The shared request/response popup (Map, Scanner,
  Discover) has a **⧉ Copy URL** button in the header.
- **Scanner findings open flow popup.** Clicking an affected target (or an active-scan
  finding) opens the same request/response inspect modal as Map — no jump to History.
- **Intruder: “Race” renamed to Null.** The no-payload attack mode (verbatim resend ×N)
  is labeled **Null** in the UI — useful for duplicate submits, idempotency, rate
  limits, and concurrent replays, not only race conditions. API accepts
  `attackType: "null"` as an alias for `"repeat"`.
- **Repeater request body Raw/Pretty + encode menu.** Body has Raw/Pretty like the
  response pane; Send always compacts JSON to raw wire form. Right-click selected
  text in the URL, headers, or body for URL/Base64/Hex/HTML encode & decode.
- **Proxy History export opens Save As.** The Export button fetches the HAR and
  prompts for a filename (native save dialog on Chromium; download fallback elsewhere).
- **Proxy History: no Refresh button.** History updates live over SSE (`flow.new` /
  `flow.update`); the manual refresh control was removed. Removed the redundant
  “right-click a row to filter” toolbar hint. The **views…** dropdown stays hidden until
  at least one saved view exists.
- **Desktop-first layout.** Toolbars stay on one row (horizontal scroll instead of
  wrapping), the inspector keeps side-by-side request/response panes at normal window
  sizes, and the **?** shortcuts panel uses a four-column card grid instead of a
  narrow vertical list.

## [0.8.0] — 2026-06-26

### Changed
- **Performance: faster History and Map.** Flow list API skips header JSON blobs
  (`QueryFlowsListFilter`) and returns `truncated: true` when the limit is hit; the UI
  shows a **500-flow cap** banner. SSE `flow.new` / `flow.update` patch rows in place
  instead of reloading the whole table when filters are simple. Map `/api/endpoints` is
  cached until flows change. Intercept SSE omits held `raw` bodies (lazy-fetch via
  `GET /api/intercept/held/{id}/raw`); WS frame events are debounced per flow.
- **Search uses FTS5.** Flow search (`?search=`) now matches via an FTS5 index on
  host/path/method/note (prefix tokens); indexes stay in sync on insert/update/delete.
- **Body GC and SQLite pool.** `GCBodies` uses a DISTINCT hash union; the DB pool
  allows a few concurrent readers (`SetMaxOpenConns(4)`).
- **UX polish.** Top bar **⌘K** and **?** buttons; Views dropdown always visible;
  **Ctrl+Space** sends only on the Repeater tab; Authz in the command palette;
  toast `aria-live="polite"`; context-menu arrow-key nav; intercept filter auto-applies
  (~650ms debounce); Match & Replace `<details>` opens when rules exist; **Ctrl+Shift+F**
  forwards held traffic (avoids clashing with browser Find); Activity rows with a flow
  reference jump to History; AI streaming throttles Markdown re-renders; Notes preview
  caches rendered HTML; Proxy/Intercept stack below ~1100px width.
- **Project Notes images in SQLite.** Pasted screenshots are stored as blobs in a new
  `notes_images` table; markdown keeps a short `![…](/api/notes/images/{id})` ref instead
  of inline base64. Preview loads images from the API. Legacy data-URL images are migrated
  automatically on load/save; orphaned blobs are garbage-collected when notes change.
- **Project Notes autosave + shortcut.** Notes save automatically (~800ms after you
  stop typing, on blur, and when leaving the tab or switching to Preview). The manual
  Save button is gone; a small status shows saving/saved. **Ctrl+B** jumps to Notes
  (also in the command palette).
- **OpenRouter AI settings: validated key + model picker.** Settings → AI assist now loads
  models into a **dropdown** (no free-text model IDs). **Load models** validates the API key
  against OpenRouter's `/auth/key` endpoint; **Save** rejects invalid keys and unknown models.
  Anthropic still uses an optional text model field.
- **Proxy History multi-select without checkboxes.** Row checkboxes and the header
  select-all box are gone — **Shift+click** (or **Shift+j/k**) range-selects,
  **Ctrl+Shift+click** (or **Ctrl+Shift+j/k**) toggles a row, and **Ctrl+Shift+A**
  selects or clears all shown flows. Selected rows are highlighted; the action bar
  is unchanged.
- **Map tab overhaul (site-map UX).** **Tree** is now the default view (remembered in
  `localStorage`); new **Table** view with sortable Method/Path/Status/Hits columns, row
  click → inspect, **→ Rep** → Repeater. **Graph** is optional: larger click targets,
  hover tooltips, search highlights + auto-expand, breadcrumb bar, double-click a host to
  focus its domain, and a warning when the graph exceeds 150 nodes.
- **Request/response views default to Pretty.** The inspector, the Repeater response pane, and
  the Map flow popup now beautify (and syntax-highlight) the body by default instead of showing
  raw bytes — toggle back to **Raw** anytime. Large bodies still fall back to raw automatically.
- **Context menus are now genuinely contextual.** Right-clicking a history row builds a
  menu keyed to the **column you clicked**, with a persistent global section underneath:
  - **Host** → filter/exclude this host, **filter domain** + **add domain to scope** (`*.example.com`,
    with a registrable-domain heuristic that handles `co.uk`-style suffixes; suppressed for IP
    literals), add host to scope, **🔎 Discover content** (prefills the Discover tab for that host),
    and delete-all-from-host.
  - **Status** → filter status class / exclude this exact status (no host clutter).
  - **Method / Path** → their own filter/exclude (path adds copy-path).
  - **Global section** (always): Send to Repeater/Intruder, Copy URL/cURL, ✨ Ask AI explain/payloads,
    🔓 Authz test, **🔑 Use as login macro**, Clear filters.
- **The request/response inspector panes now have their own right-click menu.** A
  **Selection** section (Copy, Decode/encode, Search in history, Add to scope when the text looks
  like a host) appears when text is highlighted, above the same global flow actions; with no
  selection it offers Open Decoder. Robust selection read (falls back to the range text).

### Added
- **Content discovery / forced-browse (new `Discover` tab).** A scope-aware
  directory/file brute-forcer (think dirbuster/gobuster/ffuf, built in). Point it at
  a base URL and it works a wordlist (a curated default ships built-in; paste your own
  to go deeper), with **per-directory soft-404 calibration** so it doesn't drown in
  false hits, optional **extensions** (`.php .bak …`), **recursion** into discovered
  directories (depth-limited), a bounded **worker pool** (threads + delay, same model as
  Intruder), and a manual **length filter**. Found paths stream into the results table
  live and — by default — are re-issued and recorded as flows tagged `FlagDiscovery`, so
  they also populate **History and the Map**. New engine in `internal/discovery` (fully
  unit-tested), wired through `internal/control` (`/api/discovery/{start,stop,state,wordlist}`)
  with SSE updates. Every probe is gated by target **scope** (with includes set, recursion
  can't wander off-scope; with none, the operator-typed base URL is taken as authorization).
- **Project selection is now entirely in the UI.** The top-bar project badge is a
  clickable **Projects** picker (also reachable from the badge with <kbd>Enter</kbd>/<kbd>Space</kbd>):
  it shows the active project and its data dir, lists every saved project as a one-click
  **switch** row, and has a **Create & open** field for a new project. Switching reuses the
  existing `/api/project/switch` restart-and-reconnect flow. Selecting, creating, and switching
  projects no longer requires opening Settings.
- **Last-active project is remembered across restarts.** The active project name is recorded in
  `~/.interceptor/active-project`, so a plain `interceptor` launch resumes whatever the UI last
  switched to (rather than always starting on `default`). An explicit `--project` still wins, and a
  `--project /path` one-off never overwrites the remembered name.
- **Login macro + 401 re-auth (session continuity).** Settings → Session gains a **login macro**:
  record a login request (or right-click a flow → **Use as login macro**), run it to extract
  `Cookie` / `Authorization` into session headers, optional **refresh interval**, and **re-auth on
  401** (Repeater/Intruder automatically re-login and retry once). API: `POST /api/session/login/run`,
  `POST /api/session/login/from-flow/{id}`; MCP: `run_login_macro`.
- **Discovery loop completion.** History shows a **DSC** badge on `FlagDiscovery` flows and a
  **🔎 discover** filter; Discover results have **→ Rep** (send to Repeater); **◎ From scope** fills
  base URLs from include rules; **＋ History seeds** and **✨ AI paths** grow the wordlist from
  captured traffic (+ optional AI). API: `/api/discovery/seeds`, `/suggest`, `/scope-targets`.
- **MCP discovery tools.** `start_discovery`, `discovery_state`, `stop_discovery`,
  `suggest_discovery_paths` — **41 tools** total (descriptor test guards drift).
- **UX polish.** Top bar shows **intercept state** (REQ/RESP on + held count); Proxy history adds
  `j`/`k` row walk and bare `r` → Repeater; improved empty states on Discover and Authz.
- **Docs:** [MCP cookbook](docs/product/mcp-cookbook.md) (3 agent recipes) and
  [benchmark comparison vs Burp/ZAP](docs/product/benchmark-comparison.md).
- **`interceptor update` CLI.** Self-update from the terminal: downloads a prebuilt
  release binary for your OS/arch from GitHub (with `checksums.txt` verification when
  present), replaces the running executable, or falls back to `go install` when the
  release has no binary attached. Flags: `--check`, `--version vX.Y.Z`, `--force`.
  Also: `interceptor help`, and the startup update notice now suggests the command.

### Changed
- **No more terminal project picker.** Startup no longer blocks on the interactive
  "choose a project" prompt (`1) New · 2) Continue · [Enter] Default · q) Quit`). Interceptor
  now boots straight into a project — the resolved `--project`/`INTERCEPTOR_PROJECT`, else the
  remembered last project, else `default` — and all project management happens in the web UI.
  The `INTERCEPTOR_NO_PROMPT` env var is obsolete (there is no prompt to suppress).

## [0.7.0] — 2026-06-26

### Added
- **Intruder: Race mode + concurrency + throttle (race-condition testing).** A new **Race** attack
  type sends the request verbatim N times with **no payloads or § markers** required. The attack bar
  gains **threads** (max concurrent in-flight requests, 1–64) and **delay** (ms between dispatches)
  controls that apply to every mode. Set high threads + 0 ms delay to fire requests together and hit
  a race window; set 1 thread + a delay to throttle. Backend: `intruder.Spec` gains `Repeat`,
  `Threads`, `DelayMs`; the engine now runs jobs through a bounded worker pool (verified: 8 sends ×
  8 threads complete concurrently). Tested in `internal/intruder`.
- **AI assist: streaming, Markdown rendering, and one-click actions.** The assist modal
  no longer stalls on a full completion or dumps raw text:
  - **Streaming.** Explain / Summary now stream the model's reply token-by-token over a new
    `POST /api/ai/assist/stream` SSE endpoint (`aiassist.CompleteStream` for both the Anthropic
    and OpenRouter providers), re-rendering live as it arrives. Falls back to the non-streaming
    `POST /api/ai/assist` if the stream can't be opened. A **Stop** button aborts mid-stream.
  - **Markdown.** Replies render through the existing safe `renderMD` (headings, lists, code,
    bold, links) instead of plain text.
  - **Actionable payloads.** Payloads mode calls a new `POST /api/ai/actions` that returns
    structured suggestions (`{point, payload, why}`, JSON tolerant of stray prose/fences) rendered
    as cards: copy a payload, send one **→ Intruder**, or **⚑ Load all → Intruder**. Loading
    stages the request + payloads in Intruder for the user to mark `§` and Start — it never
    auto-fires attacks (consistent with active-scan's arm gate).
  - **Flow actions.** A footer bar turns the analysed request into one-click **→ Repeater** /
    **→ Intruder** loads, plus **Copy**.
- **UI: how-to-trust-the-CA instructions (Settings → TLS / CA).** The CA download now sits above
  collapsible per-platform trust steps (macOS, Windows, Firefox, iOS, Android) — closing the main
  first-run gap for HTTPS interception — and notes that plain HTTP needs no CA.
- **UI: AI assist reachable from the History right-click menu.** Added **✨ Ask AI → explain** and
  **✨ Ask AI → payloads** items, so the assistant isn't only behind the inspector's ✨ button. The
  onboarding card now mentions the AI action and the `Ctrl/⌘ K` command palette.
- **UI: keyboard-shortcut cheatsheet.** Press <kbd>?</kbd> for an overlay listing every shortcut
  (palette, search, row nav, send-to-Repeater/Intruder, forward/drop, …) — previously all hidden.
- **UI: inspector action bar.** The request pane header now has **→ Rep**, **→ Intr**, and **cURL**
  buttons next to ✨, so the core capture→act workflow is discoverable without the right-click menu.
- **UI: capture-liveness indicator.** The top-bar proxy dot now pulses on each captured request and
  a status reads *waiting for traffic* → *capturing live* → *idle · N captured this session*, so it's
  clear whether the proxy is actually receiving traffic.
- **UI: Map interaction hint** (drag/zoom/click) and refreshed AI-assist settings copy (mentions
  streaming, the right-click entry, and loading payloads into Intruder).
- **UI: binary response/request bodies show headers only.** Images, fonts, media, archives,
  PDFs and other non-text bodies (by Content-Type) no longer dump unreadable bytes into the
  inspector or Map popup — the header block renders (rebuilt from the flow detail, so the body
  isn't even fetched) with a size note, a **Download body** link, and a **Show raw anyway**
  escape hatch.
- **AI payloads recommend Repeater vs Intruder.** The actions endpoint now tags each suggestion
  with a `tool`: **Repeater** for a one-shot manual probe (auth/authz bypass, a specific IDOR
  value, an SSRF/logic test — send one crafted request and read the response) or **Intruder** for
  fuzzing/enumeration over many values. Each payload card surfaces the recommended tool as its
  primary button; **→ Repeater** loads the request and copies the payload to paste at the
  injection point, **→ Intruder** stages it for fuzzing. (Previously everything went to Intruder.)
- **UI: DATA & RETENTION panel in Settings → Project & data.**
  A new card (below EXPORT / IMPORT) surfaces the `GET /api/hosts/stats` data as an
  interactive per-host table: checkboxes to select hosts, per-row flow count and size
  (formatted with the `fmtBytes` helper matching backend `B/KB/MB/GB` thresholds), and
  a per-row **Delete** button. Bulk actions: **Delete selected** (`mode=delete`) and
  **Keep only selected** (`mode=keepOnly`; disabled/warned when nothing is checked to
  prevent the server-rejected empty-list 400). A free-text **Purge by pattern** input
  supports wildcard patterns like `*.ads.example.com`. A **Reclaim space** button calls
  `POST /api/flows/gc` and toasts the freed bytes. Every destructive action goes through
  a themed `uiConfirm()` in-app dialog (new `#confirmModal`, reusing the same modal
  plumbing as `uiPrompt`) that names the host(s), flow count, and warns the deletion is
  permanent. Stats are loaded lazily the first time the Project section is opened (not
  on every tab switch) and refreshed after every purge/GC. The History list (`loadFlows`)
  is also refreshed after purge so the Proxy tab updates immediately alongside the SSE
  `flow.new` broadcast.
- **UI: "🗑 Delete all from &lt;host&gt;" in the History right-click context menu.**
  A new destructive item (after the Send-to-Repeater/Intruder group, visually separated
  by a `ctx-sep` divider, text and icons colored `var(--red)`) opens the `uiConfirm`
  dialog naming the host and flow count before calling `POST /api/flows/purge`. After
  confirmation it refreshes both the retention panel and the History list.
- **UI: `fmtBytes` JS helper.** Matches the backend/MCP byte-format convention exactly:
  `< 1 KB → "N B"`, `< 1 MB → "N.N KB"`, `< 1 GB → "N.N MB"`, else `"N.N GB"`.
  Used by the retention panel and context-menu purge toasts so numbers agree with MCP
  tool output.
- **control + mcp: data-retention REST API and MCP tools.**
  Three new REST endpoints:
  `POST /api/flows/purge` deletes flows by host pattern (`mode=delete`) or keeps only the listed hosts (`mode=keepOnly`), always runs `GCBodies` afterward, and broadcasts an SSE `flow.new` reload signal so open History views refresh live. Returns `{deleted, removedFiles, freedBytes}`.
  `POST /api/flows/gc` is a standalone GC trigger (reclaims orphaned body files, no flows deleted). Returns `{removedFiles, freedBytes}`.
  `GET /api/hosts/stats` returns per-host flow counts and byte totals sorted descending by bytes, plus `totalFlows` and `totalBytes`.
  Two new MCP tools: `host_stats` (readable text table of host·flows·size) and `prune_history` (parses comma/newline-separated host patterns, POSTs to `/api/flows/purge`, returns a concise summary like `deleted 42 flows · freed 1.3 MB`; documented as destructive). Both tools set `X-Interceptor-Source: ai` (existing MCP plumbing) so purges appear in the Activity feed. A `formatBytes` helper in `internal/mcp` renders byte counts as `B / KB / MB / GB` (same thresholds the UI should match). MCP `instructions` string updated with the `host_stats`→`prune_history` workflow note.
- **store: data-retention primitives.** Three new store-layer methods:
  `DeleteFlowsByHost(hosts []string, keepOnly bool)` deletes flows by wildcard-aware
  host pattern (exact or `*.acme.com`) in delete-matching or keep-only mode; an empty
  keep-list is rejected with an error to prevent accidental data wipe.
  `GCBodies() (removedFiles, freedBytes int64)` removes content-addressed body files
  in `bodiesDir` that are no longer referenced by any flow's `req_body_hash` or
  `res_body_hash`; safe to run live (never touches referenced or non-hash files).
  `HostStats() []HostStat` returns per-host flow counts and approximate byte totals
  (SUM of per-flow lengths; approximation because deduped bodies are counted once per
  referencing flow), sorted descending by bytes — for a retention-UI size breakdown.
- **UI: accessible tab bar.** `#tabs` now carries `role="tablist"`; each `.tab` button
  carries `role="tab"`, `aria-selected`, `aria-controls`, and a matching `id`. Each
  panel carries `role="tabpanel"` and `aria-labelledby`. Roving tabindex: only the
  active tab is in the tab sequence; Left/Right arrows move focus between tabs and
  activate them (standard ARIA tablist pattern).
- **UI: modal ARIA + focus trap.** Every dialog overlay (`#flowModal`, `#aiModal`,
  `#checksModal`, `#activeModal`, `#decModal`, `#promptModal`) now has
  `role="dialog" aria-modal="true"` on its inner card and an `aria-labelledby`
  pointing at its title element. A shared `openModal`/`closeModal` helper moves focus
  into the dialog on open (first button), traps Tab/Shift+Tab within the focusable
  elements, and restores focus to the triggering element on close. The existing Escape
  and backdrop-click behaviour is preserved.
- **UI: `aria-pressed` on toggle buttons.** All `.toggle` state buttons (intercept
  on/off, response intercept, system proxy, capture-scope, browser-telemetry
  suppression) now set `aria-pressed="true/false"` whenever their `.on` class is
  toggled, so screen readers announce the current state.
- **UI: `aria-label` on icon-only / emoji buttons.** `#aiExplainBtn` (✨),
  `#aiPulse` (now `role="button" tabindex="0"`), `#mapRefresh` (↻), `#mapFit` (⤢),
  `#checksBtn` (✎), `#activeBtn` (⚡), and the proxy `#refreshBtn` all carry
  descriptive `aria-label` attributes mirroring their existing `title` text.
- **UI: resizable inspector splitter.** A thin drag handle (`#inspectSplitter`) sits
  between the history table (`#rows`) and the request/response inspector (`#inspect`)
  on the Proxy panel. Dragging it adjusts the inspector height (clamped to 120 px –
  80 % of the panel). Height is persisted to `localStorage` under the key
  `inspect.height` and restored on load. The handle carries
  `role="separator" aria-orientation="horizontal" tabindex="0"`; Up/Down arrows nudge
  the height by 20 px for keyboard-only access. Styled with `--line`/`--accent` CSS
  variables — no hardcoded colours.

### Added
- **Intruder grep-match / grep-extract + payload processing.** Two new fields flag a result when its
  **response matches** a regex/text (shown ✓ + row highlight) and **extract** a regex group from each
  response (shown inline, e.g. a token or balance) — turning Intruder from a status-anomaly sender
  into a real fuzzer. A **payload processing** field transforms each payload before sending
  (`urlencode`, `base64`, `upper`, `lower`, `prefix:X`, `suffix:X`, comma-separated, in order); the
  label keeps the original while the processed value goes on the wire. Persisted per attack tab.
  Tested in `internal/intruder`.
- **Authorization (access-control) testing.** Right-click a flow → **🔓 Authz test** to replay it
  under each saved identity (role) and diff the responses. The first identity is the baseline (your
  privileged user); any lower-privileged role that still gets a successful, ~same-size response is
  flagged **⚠ same access** — a strong IDOR / broken-access-control signal (OWASP #1). Identities are
  named header sets (Cookie/Authorization; blank = anonymous), persisted server-side; replays use the
  identity's auth only (the global session/macro is skipped via a new `NoSession` send flag) and are
  recorded as `FlagAuthz` flows. New `internal/control/authz.go` + `/api/authz[/run]`.
- **Session token macro (CSRF / re-auth).** Settings → Session now has a **token macro**: a refresh
  request (raw HTTP + target) sent before each Repeater/Intruder/scan send, whose response is matched
  by a regex (one capture group) and injected — either as a **header** or by replacing a **`§placeholder§`**
  in the outgoing request. Keeps requests valid against apps that rotate a CSRF token per request or
  expire sessions. The refresh uses a plain client (never recorded, never recursive). Tested in
  `internal/sender` (fresh token injected per send).
- **Out-of-band (OOB) interaction catcher — blind-vuln detection.** New `internal/oob` catcher mints
  unique tokens and records any inbound request to `/oob/<token>/…` (method, path, query, source,
  user-agent, body preview). A Scanner → **⚲ OOB** modal generates a copy-ready payload URL, lets you
  set a target-reachable base URL (your LAN IP / tunnel; defaults to the control origin for local
  testing), and shows interactions **live** (SSE `oob.update`). Drop the URL into an SSRF param, XXE
  entity, or SQLi exfil and watch the target call back — proof of a blind bug. The `/oob/` endpoint
  bypasses the loopback/CSRF guard (it must accept foreign blind callers) but only records metadata.
  Tested in `internal/oob`.
- **Intruder: multiple attack tabs + run history (like Repeater).** A tab strip holds independent
  saved attack configs (target, template, attack type, threads/delay/repeat, and per-marker payload
  lists), persisted to `localStorage` and restored on reload; titles derive from type + host. A
  collapsible **⟲ History** rail records every completed run this session (type, request count,
  flagged count, target) — click an entry to re-open both its results and the exact config that
  produced it.

### Changed
- **UI: Intruder payload lists are now per-marker (Pitchfork).** Instead of two fixed payload boxes,
  the PAYLOADS area renders **one colour-coded input per `§` marker** in the template — add a 3rd
  marker, a 3rd list appears. Each input is labelled with its position and the marker's current text
  (e.g. "§1 · user", "§2 · pass") and carries a matching colour swatch/top-border so it's clear which
  list feeds which injection point. The header shows per-position counts ("§1:3 · §2:3 · §3:0") and
  Start refuses to run until every position has payloads. Sniper keeps its single shared list. (AI
  "load into Intruder" now seeds the Sniper list via a dedicated `setSniperPayloads` export.)
- **UI: Intruder tab redesigned.** A cleaner attack bar (target · Sniper/Pitchfork · **§ Mark
  selection** · live payload/request count · Start), a **mode explainer** line that updates with the
  selected attack type (so Sniper vs Pitchfork is self-documenting), a live **payload count** on the
  PAYLOADS header and Start button, and a results pane with a **progress bar**, a flagged-count
  summary ("N sent · M flagged ⚑"), and a clear empty state. Start now refuses an empty payload list.
- **UI: Repeater tab redesigned.** Replaced the cramped toolbar + always-on 180px history sidebar
  with a clean **request line** (method · full-width URL · History · Send-with-Ctrl+Space hint), a
  full-width Request/Response split (HEADERS + BODY on the left, response on the right), and a
  **collapsible** per-tab history rail (hidden by default with a live "⟲ History (N)" count, so the
  editor gets the full width). The response header now shows a rich status line — **code · time ·
  size** (e.g. "200 OK · 142 ms · 4.1 KB") — instead of just the status code.
- **UI: Intercept tab redesigned as a proper workspace.** Replaced the flat four-section vertical
  scroll (which buried held requests in a cramped 200px textarea and duplicated request/response
  sections) with: a bold control strip whose **Requests/Responses** switches show a live pulsing
  state, a single **unified hold queue** (requests + responses, tagged REQ/RESP) in a sidebar, a
  full-height **raw editor** with prominent **▶ Forward** / **✕ Drop** in its header, a clear empty
  state explaining the feature, and **Match & Replace** moved into a collapsible footer. Selecting a
  queue item loads it into the one editor; Forward/Drop route to the request or response API by the
  item's side. (`state.heldSel = {id, side}` replaces the separate `selHeld`/`selRespHeld`.)
- **UI: Repeater now states its purpose.** A one-line intent hint ("Edit & resend a request… Load
  one via right-click a flow → Repeater. Each tab keeps its own send history.") so a first-time
  user understands the tab without prior Burp knowledge — matching the intent lines other tabs
  already carry (Scanner, Map, Intercept, Notes, Activity).
- **UI: clearer button hover affordance.** Neutral `.btn`s now shift background to `--line` on
  hover (with a short transition); accent buttons keep their brightness lift. Makes every toolbar
  and dialog button visibly interactive.
- **Command palette (Ctrl/⌘ K) is now navigation-only and covers Settings subsections.** It jumps
  to a tab, a Settings subsection (Proxy & network / TLS-CA / Target scope / AI assist / Session /
  Project & data), or a tool screen — and never performs a mutating action (run scan, toggle
  intercept, export, send a request), so a mis-typed Enter can't do anything destructive; you act
  from the screen it takes you to. Keyword aliases (e.g. "download ca certs", "api key", "retention")
  find the right destination.
- **AI assist: faster, tighter answers.** The system prompt now demands brevity (≤~150 words
  / 6 bullets, no preamble), `max_tokens` dropped 1024 → 768, and the single-flow prompt budget
  trimmed 4000 → 2500 bytes — together cutting both time-to-first-token and total generation time
  on top of the existing streaming.
- **Browser telemetry suppression (on by default).** Chrome and Firefox background
  traffic — Safe Browsing lookups, update pings, crash reports, Normandy experiments,
  captive-portal probes — is now silently forwarded without being written to history
  or held by the intercept gate. Toggle under **Settings → Proxy & network → Browser
  Telemetry** to allow it in when you specifically need to inspect browser background
  traffic. The list of suppressed hosts lives in `internal/proxy/telemetry.go`.

### Changed
- **UI: split the monolithic `index.html` into ES modules (no build step).** The single
  ~2,700-line `internal/control/ui/index.html` is now an `index.html` shell + `app.css` +
  native ES modules under `ui/js/`: `core.js` (shared foundation — DOM helpers, the `state`
  object, `api()`, formatters, HTTP highlighters, the modal system, `renderMD`/`accordionize`)
  and one module per feature (`proxy`, `intercept`, `tools`, `scanner`, `map`, `settings`,
  `notes`, `apipanel`, `activity`, `ai`), wired by an `app.js` entry that owns tabs, the command
  palette, shortcuts, the SSE stream, and boot. Behaviour is unchanged. The `//go:embed` now
  embeds the whole `ui/` directory and `serveUI` serves the static assets with **explicit**
  `Content-Type`s (the OS mime registry can resolve `.js` to `text/plain` on Windows, which makes
  browsers refuse to execute ES modules). No bundler or toolchain added; the binary stays single
  and static.
- **UI: visible keyboard focus ring.** The global `outline:none` on form elements
  previously killed all browser focus indicators. A `:focus-visible` rule now restores
  a 2 px accent-coloured ring (using `--accent`) on keyboard navigation while
  suppressing the ring on mouse clicks — keeping the desktop look clean.
- **UI: responsive toolbars.** `.toolbar` rows now `flex-wrap:wrap` with a `row-gap`
  so controls spill onto a second line on narrow windows instead of clipping. `#tabs`
  gains `overflow-x:auto` so the AI-pulse / version badges remain reachable when the
  window is tight. A `@media (max-width:900px)` block relaxes `.search` max-width and
  adjusts padding.

### Fixed
- **Segmented toggles on Intruder / Repeater / AI / Map were dead (didn't switch).** After the UI
  was split into ES modules, `proxy.js` ran *after* the feature modules (they're imported by it),
  so its broad `$$('.seg')` inspector wiring **clobbered** every other tab's seg handlers — leaving
  Intruder's Sniper/Pitchfork, Repeater's Raw/Pretty, the AI modal's Explain/Payloads/Summary, and
  the Map's Graph/Tree visually toggling but doing nothing. Scoped that wiring to the inspector's own
  segs (`.seg[data-side]`) so each module keeps its own handler. (Pitchfork now reveals the second
  payload list as expected.)
- **Map: the graph re-fits to the viewport on every search/filter change, and the result count
  is never blank.** Previously a search left the graph at its old pan/zoom (matches could sit
  off-screen) and the `#mapCount` label went empty when nothing matched, so you couldn't tell if
  there were results. Now any search / domain / method / status / expand change re-fits the graph,
  the count always shows ("N endpoints · M hosts", "No endpoints match the filters", or "No
  endpoints captured yet"), and the empty graph resets its transform so the message is visible.
- **Light theme now meets WCAG AA contrast on every surface.** A measured audit found the light
  palette failed AA for secondary text (`--fg3`), the accent used as text/buttons, `--cyan`, and
  `--amber`/`--red` on the darker `--bg3`. Darkened `--fg3` (#787f8c→#5e6675), `--accent`
  (#00a368→#00734a, also lifting white-on-accent button text to AA), `--blue`, `--amber`, `--red`,
  and `--cyan`, and aligned `--sel`/`--accentDim`/`--redDim` to the new tones. The dark theme already
  passed and is unchanged.
- **UI assets are no longer cached stale.** `serveUI` now sends `Cache-Control: no-store`
  on the index shell and every JS/CSS module. Without it, browsers heuristically cached the
  un-versioned ES modules, so users (and the AI-assist mode tabs) kept running an old build
  after an upgrade until a manual hard-refresh.
- **AI assist: switching modes mid-request no longer leaves stale output.** A per-request
  sequence guard means a superseded Explain/Summary stream or Payloads fetch can't write over
  the mode you switched to, and the modal header now wraps so the Explain/Payloads/Summary
  tabs can't overflow off a narrow dialog.
- **UI: hardcoded `rgba(255,80,80,.08)` in active-scan warning box.** The translucent
  red fill bypassed the theme system and looked wrong in light mode. Introduced
  `--redDim` in both `:root` blocks (dark: `rgba(255,80,80,.08)`, light:
  `rgba(207,58,58,.08)`) and replaced the inline literal with `var(--redDim)`.
- **UI: animations respect `prefers-reduced-motion`.** The `blink` (1.6 s) and
  `pulse` (1 s) keyframe animations now set `animation:none` inside a
  `@media (prefers-reduced-motion:reduce)` block, eliminating motion for users who
  have requested it at the OS level.

## [0.6.0] — 2026-06-25

### Added
- **Optional API-key auth for the MCP endpoint.** Opt-in: with no keys the control
  plane stays loopback-only (unchanged); once you create a key (**API** tab), the
  Streamable-HTTP **`/mcp`** endpoint requires `Authorization: Bearer <key>`, so a
  hosted/remote agent must authenticate. `/api` stays loopback-trust. This wires up
  the previously-dormant key verification.

### Changed
- **The open tab is remembered across refreshes.** Reloading the UI no longer
  bounces you back to **Proxy** — it reopens whichever tab you were on (Map, Notes,
  Scanner, …), persisted in `localStorage`.
- **Map "All domains" view is now an overview, not a wall.** Selecting *All domains*
  collapses every host to a single node tagged with its endpoint count (`+N`); click a
  host to drill in. Picking a specific domain still shows its tree fully expanded —
  keeping the graph readable across dozens of hosts instead of cramming hundreds of nodes.
- **Map: click an endpoint to pop up its request/response.** Clicking an endpoint
  node (graph or tree) now opens a quick Raw/Pretty request+response viewer in a
  modal — with an **All in Proxy ↗** button that filters History to *every* request
  to that endpoint (host + method + path) — instead of yanking you to the Proxy tab.
- **The UI no longer auto-opens by default.** A plain `interceptor` start is now
  quiet — friendlier for restarts and headless/daemon use. Pass **`--open`** (or set
  `INTERCEPTOR_OPEN_BROWSER`) to launch the browser; `INTERCEPTOR_NO_BROWSER` still
  hard-disables it. The UI URL is printed on startup.
- **AI activity is persisted (survives restart).** The glass-box Activity feed was
  an in-memory session ring — lost on restart and not per-project. It's now stored
  in the project database (an `activity` table, capped at ~5000 rows) and reloads
  with the project. Backed by `store.InsertActivity` / `ListActivity`.
- **Endpoint map filters match the Proxy bar.** The Map's status filter is now a
  dropdown (`status / 2xx / 3xx / 4xx / 5xx`) like Proxy's, next to the method and
  search controls — replacing the bespoke toggle buttons.
- **Endpoint map is now a node-link graph.** The Map tab defaults to a visual
  `domain → path → endpoint` graph (hierarchical tidy-tree layout in SVG) you can
  **pan** (drag), **zoom** (wheel), **Fit**, and **collapse** node-by-node; nodes
  are coloured by host/path/endpoint with status-coloured endpoint markers, and
  clicking an endpoint jumps to its flow in Proxy. It opens focused on **one
  domain** (the busiest) chosen via a **Domain** picker — switch domains or pick
  *All* — so a large attack surface stays readable instead of cramming every host
  on screen. A **Tree / Graph** toggle keeps the compact accordion list available;
  the domain/status/method/search filters apply to both.

### Fixed
- **MCP `intruder_state` / `list_ws_frames` / `active_scan_state` now return valid,
  bounded JSON.** They byte-truncated the JSON payload mid-structure — output an agent
  couldn't parse, exactly when results were large and interesting. They now cap the
  result rows and stay parseable, with `_truncated` / `_total` markers.
- **The Activity "Clear" button actually clears.** Now that the feed is persisted, a
  client-only clear reappeared on reload; Clear now deletes the rows
  (`DELETE /api/activity`) and tells live clients to drop their copy.
- **Quote-safe HTML attribute escaping.** A `"` or `'` in a match-&-replace regex, a
  scope rule, or a captured host/path can no longer break out of the surrounding
  attribute — a new `escAttr()` escapes quotes in attribute slots (the JSON/HTTP body
  highlighters keep the quote-preserving `esc()`).
- **Docs match reality:** the MCP toolset is **36 tools** (was mislabelled 24) across
  the README, roadmap, and the in-app **API → MCP** descriptor — now covered by a test
  so it can't drift again; PRD-0002 (active scanning) is marked **shipped**.
- `gofmt` brought clean across the tree (three long-standing files).

### Security
- **`/api/project/switch` is restricted to plain project names.** A loopback request
  could previously pass a filesystem path (`/tmp/…`, `~/…`, `../…`) that the re-exec
  turned into `MkdirAll` + a process relocation to an arbitrary directory. The network
  switch now rejects anything but a bare name; the local `--project` CLI flag still
  accepts paths.

## [0.5.0] — 2026-06-24

### Added
- **Project notes (markdown notebook).** A new **Notes** tab gives each project a
  shared markdown scratchpad — for credentials, findings, scope, and to-dos — with
  an **Edit / Preview** toggle (a small, XSS-safe markdown renderer). The AI shares
  the same notebook: new MCP tools **`get_notes`**, **`set_notes`**, and
  **`append_notes`** let the assistant read it and record findings into it, so you
  and the AI work from one set of notes. Stored per-project (a `project.notes`
  setting), exposed via `GET`/`PUT /api/notes`, and synced live across open tabs
  with a `notes.update` event. Notes also embed **images** — paste a screenshot
  straight into the editor (stored inline as a data URL) or use `![alt](url)` — and
  render each heading as a **collapsible accordion** section, so long notes fold
  into title/subtitle blocks you can open and close.
- **Scope-only capture (saves DB space).** Settings → Proxy → **Capture policy**
  switches between persisting **all** proxied traffic (default) and **only
  in-scope** traffic. Out-of-scope requests are still forwarded, but neither their
  metadata **nor their bodies** (the bulk of disk use) are written — so a long
  engagement through a busy browser doesn't bloat the project database with
  CDN/analytics noise. The proxy gates persistence and body capture on scope;
  interception is unaffected, and with no scope rules set everything is in scope
  (so it's a no-op until you define a target). Backed by a `capture.scopeOnly`
  setting (restored on restart) and a proxy `persistable`/`teeBody` gate.
- **Endpoint map (attack surface).** A new **Map** tab renders the captured
  traffic as a collapsible `domain → path → endpoint` tree, so you can see an
  app's structure at a glance. Endpoints are de-duplicated — repeated hits (and
  noise like dozens of 404s) collapse into one node carrying a hit count and the
  distinct statuses seen, coloured by status. Filter by **path/host search**,
  **method**, and **status-class toggles** (mute 4xx/5xx noise); a search
  auto-expands to reveal matches, and clicking an endpoint jumps to its flow in
  Proxy. Backed by a new `store.Endpoints` aggregation (`GROUP BY host,method,path`,
  excluding Intruder/active-scan traffic) and `GET /api/endpoints`.
- **JSON bodies are syntax-highlighted in Pretty view.** With **Pretty** selected,
  the request/response body now colour-codes JSON — keys, string values, numbers,
  and `true`/`false`/`null` each get their own colour (the start line, headers, and
  status code were already coloured) — so a large payload is far easier to scan.
  The body is HTML-escaped before tokenizing, so highlighting stays XSS-safe even
  on hostile captured content.
- **Multi-select & bulk actions in History.** A checkbox on each row (with
  **shift-click** range select and a **select-all** header box) lets you pick
  multiple flows and act on them from a selection bar: **Delete** them (two-click
  "Confirm?" arm — no browser dialog), **Ask AI** to analyze the whole selection
  together, or **Add to scope** (adds the selected hosts to target scope). Backed
  by `store.DeleteFlows`, `POST /api/flows/delete`, and a multi-flow mode on
  `POST /api/ai/assist` (a `flowIds` array → a combined per-endpoint review).
- **Response time & keyboard navigation in History.** Selecting a flow now shows
  its response time next to the status in the inspector (e.g. `200 OK · 142 ms`) —
  the duration was always recorded, just never surfaced. And **↑/↓** now walk the
  History rows (loading each into the inspector) while the Proxy tab is focused, so
  you can triage traffic without the mouse. The keys are ignored while you're
  typing in a field or a modal/command-palette is open.
- **Notes on requests/responses.** Any flow in History can carry a free-text note.
  Select a flow and use the **📝 NOTE** bar in the inspector (Enter or click away
  to save); annotated rows show a 📝 marker, and notes are matched by the History
  search box. The AI shares them: `get_flow` / `list_flows` now include the note,
  and a new **`set_note`** MCP tool lets the assistant record a finding inline
  (e.g. "IDOR confirmed") for the operator — and read notes the operator left for
  it. Backed by a `note` column (added by an automatic, idempotent migration so
  existing projects upgrade in place), `PUT /api/flows/{id}/note`, and an SSE
  `flow.update` broadcast.
- **AI traffic shows in Proxy/History (glass box, part 2).** Requests the AI
  assistant issues over MCP — Repeater (`send_request`), Intruder, and active
  scan — now appear inline in the Proxy **History** view, marked with an **AI**
  badge, instead of only in the Activity feed. An operator watches the AI's actual
  requests alongside their own captured traffic. The MCP server stamps every call
  `X-Interceptor-Source: ai`; the control plane tags the resulting flows with a new
  `FlagAI` and exempts them from History's Repeater/Intruder/Active-scan exclusion
  (a new `FlowFilter.IncludeFlags` overrides `ExcludeFlags`). A **🤖 AI** toggle in
  the History toolbar (and `?ai=0` on `GET /api/flows`) hides them again. The AI's
  requests still go direct to the target (fast, no intercept deadlock); only their
  visibility changed.

### Changed
- **Oversized bodies aren't rendered (no browser lag).** A request or response
  body over 2 MB is no longer syntax-highlighted into the inspector — which could
  lag or freeze the tab — and isn't even fetched. Instead the pane shows the body
  size with a **Download raw** link and a **Show anyway** escape hatch.
- **Tidier filter & Views controls in Proxy.** *Views* are saved filter sets — so
  the **Views** picker is now hidden until you've saved one, and **＋ Save view**
  only appears when a filter is actually active (no more empty, confusing controls
  in the toolbar). The active-filter chips gained a **Clear all** pill next to the
  per-chip ✕, so you can drop every filter in one click instead of removing them
  one at a time.
- **Scanner groups findings by type.** The Scanner tab now lists one row per
  finding (e.g. "SQL Injection") with its affected-target count and severity,
  sorted High→Info; selecting it shows every affected target nested in the detail
  — each links through to its flow in Proxy — instead of a separate row per
  (finding × target). The header now reads "N findings · M targets". A description
  shared across targets is shown once; per-target detail/evidence stay inline.

## [0.4.0] — 2026-06-24

### Added
- **AI Activity feed (glass box).** A new **Activity** tab streams every action your
  AI assistant takes over MCP — tool, the gist of the arguments, the result, and
  timing — live, newest-first, so a human can watch and supervise the AI as it
  works. A pulsing **AI active** indicator in the header (with an unseen-count
  badge on the tab) shows from any tab whenever the AI acts, and clicks through to
  the feed. Backed by `mcp.Server` reporting each tool call to `POST /api/activity`,
  a session ring buffer, `GET /api/activity`, and an `activity` SSE event. Entirely
  AI-optional — manual pentesting is unchanged; the feed simply stays empty.
- **Response/request bodies are decompressed for display.** Inspector and Repeater
  views now transparently inflate `Content-Encoding: gzip / deflate / br / zstd`
  (Brotli & Zstd via pure-Go libs, no cgo) — so the body shows readable text
  instead of compressed bytes that look like undecrypted garbage. The decoded view
  drops `Content-Encoding`, corrects `Content-Length`, and adds an
  `X-Interceptor-Decoded` marker. Falls back to the raw bytes if decoding fails.
- **Switch projects from the UI.** Settings → Project now lists your projects and
  lets you open another one — or start a new one by name or absolute path — without
  restarting from the terminal. Interceptor relaunches itself onto the chosen
  project (shared CA, so no re-trust) and the UI reconnects. Backed by
  `GET /api/project` and `POST /api/project/switch`.
- **Scanner targets a chosen host/filter.** The Scanner can now be pointed at one
  host (a dropdown of everything in history) and/or a path filter, instead of always
  scanning all in-scope traffic — much faster to focus on the target you care about.

### Changed
- **Leaner MCP tool descriptions.** Rewrote the AI-facing tool/parameter
  descriptions and the server `instructions` to be tight and direct: dropped
  filler and parameter descriptions that just restated the name, and hoisted the
  shared workflow/safety conventions into the one-time `instructions`. Cuts the
  `tools/list` an AI loads each session by ~20% (≈600 tokens) with no loss of the
  operational essentials (fuzz `§…§`, active-scan `arm`, the Starlark check shape).

### Fixed
- **No more native browser dialogs; export gives feedback.** The Export (HAR),
  Export project, and CA-download buttons now show a confirmation toast, and the
  one remaining `prompt()` (naming a saved view) is replaced with a themed in-app
  dialog — for a consistent look instead of the browser's chrome.
- **`--project default` means the root project.** Switching back to "default" now
  returns to the original `~/.interceptor` project rather than creating a separate
  `projects/default`, so switching away and back never orphans your data.
- **No duplicate "default" in the project switcher.** A leftover `projects/default`
  directory (from an earlier mis-switch) no longer shows up as a second "default"
  entry — the reserved root project is listed exactly once.
- **Modals close the way you expect.** Every modal (AI assist, custom checks,
  active scan, decoder) now closes on **Escape** and on backdrop click —
  previously only the AI modal closed on a backdrop click and none responded to
  Escape.
- **Filtered-empty history no longer looks broken.** When a filter/search matches
  nothing, History shows “No flows match the current filters” with a one-click
  **Clear filters**, instead of the “no traffic yet — set up your browser”
  onboarding card (which implied capture was broken).
- **Own traffic is now fully transparent.** Requests aimed at Interceptor's own
  loopback listeners (the control plane and the proxy port) are forwarded but never
  captured, intercepted, or run through match-&-replace. Previously, pointing a
  system-wide proxy at localhost recorded the UI's own API calls — flooding History
  and feedback-looping the live-update stream — and, with intercept on, could even
  hold the UI's requests. Mirrors the active scanner's own-listener guard.
- **Light-theme contrast.** Floating elements (modals, command palette, context
  menu, toast) now use theme-aware shadow and backdrop variables instead of hard
  black, and the selected command-palette row uses the on-accent text colour — so
  light mode no longer shows harsh black drop-shadows or a near-invisible selection.
- **Editing a held request body just works.** Forwarding an intercepted request
  whose body you changed no longer truncates it to the stale `Content-Length` —
  the length is recomputed from the actual (CRLF- or LF-separated) body. Chunked
  and genuinely body-less requests are left untouched. (The response side already
  did this.)
- **In-flight detail.** Selecting a still-pending flow now shows “waiting for
  response…” in the response pane (and a `pending` status) instead of a blank
  pane; it fills in automatically when the response arrives.
- **Repeater sending feedback.** While a send is in flight the response pane shows
  a “sending…” placeholder (and status) instead of the previous response.
- **Discoverable intercept shortcuts.** The command palette (Ctrl+K) now lists
  “Forward held request (Ctrl+F)” and “Drop held request (Ctrl+D)” when a request
  is held.

## [0.3.0] — 2026-06-23

### Added
- **Projects (Burp-style).** On an interactive launch Interceptor now offers a
  startup picker — *new project*, *continue from a saved project*, or the default
  project — so captured flows, rules, scope and custom checks can be kept in
  separate per-project databases under `~/.interceptor/projects/<name>/`. Skip the
  prompt with `--project <name|path>` or `INTERCEPTOR_PROJECT`; suppress it with
  `INTERCEPTOR_NO_PROMPT`. The CA stays shared at `~/.interceptor/ca`, so switching
  projects never means re-trusting a certificate. The active project is shown in the
  startup log, a header badge, and `GET /api/version`.
- **Conditional intercept.** A regex filter on the Intercept tab holds only requests
  whose URL / headers / body / method (or anything) match, forwarding the rest
  untouched. Configurable via `POST /api/intercept/filter` and persisted across
  restarts (`intercept.filter.*` settings).
- **Intercept keyboard shortcuts.** On the Intercept tab, `Ctrl+F` forwards and
  `Ctrl+D` drops the selected held request/response (no reach for the mouse).
- **Light / dark theme toggle.** A theme switch in the top bar, persisted to
  `localStorage` and applied before first paint (no flash). Defaults to the OS
  `prefers-color-scheme`. The palette is fully CSS-variable driven.
- **Color-coded request/response.** The read-only inspector and Repeater response
  views now syntax-highlight the HTTP message — request/status line, header
  names/values, and status code (2xx/3xx/4xx/5xx) — in both raw and pretty modes.
- **Negative (exclude) history filters.** Hide flows by method / host / path /
  status, with a right-click **Exclude** quick-action on any flow cell. Exclusions
  stack, show as red `≠` chips (removable), combine with the positive filters, and
  persist in saved views. Backed by repeatable `notMethod` / `notHost` / `notPath`
  / `notStatus` query params on `GET /api/flows`.

### Changed
- **Live history rows.** A flow now appears in History the moment its request is
  sent upstream (shown pending, with a blinking `•••` and no status yet) and is
  then updated in place once the response arrives — instead of only showing up
  after the full exchange completes. Backed by a new `flow.update` SSE event and
  `store.UpdateFlow`; long-running requests are visible while in flight.
- **Settings layout redone** as spaced, bordered cards with consistent padding and
  vertical rhythm, replacing the cramped flush-divider sections.
- The `● intercepted` flag is now set only for requests the gate actually held, so
  the conditional-intercept filter no longer mislabels traffic it forwards through.

## [0.2.2] — 2026-06-23

### Added
- **Version reporting + per-run update check** — the binary now surfaces its version everywhere:
  the startup log (`Interceptor vX.Y.Z: …`), an `interceptor version` / `--version` subcommand,
  `GET /api/version`, the MCP `serverInfo`/descriptor, and a badge in the UI header. On each run it
  does a **best-effort check of the GitHub tags** for a newer release; if one exists it logs a notice
  and the header badge turns into a clickable **“↑ vX.Y.Z available.”** Non-blocking and silent on
  failure (offline is fine); opt out with `INTERCEPTOR_NO_UPDATE_CHECK=1`. Version is now centralized
  in `internal/version` (clean release tags trusted, `(devel)`/pseudo-versions fall back to the
  constant). TDD on the semver/“is-newer” logic.

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
