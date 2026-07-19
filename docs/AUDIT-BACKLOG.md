# Interseptor — Audit Backlog (CLEARED)

> **Historical snapshot** — preserved for archaeology. Counts, IA claims, and open items may be stale relative to current `main`.


## v0.29.0 post-release audit (2026-07-07) — CLEARED

A 6-agent parallel audit (each with an isolated live instance, real end-to-end
testing, and a spot-check of prior "FIXED" claims below) covering the proxy/
capture/TLS/WS core, the attack engines (Repeater/Intruder/Scanner/checks/
active-scan/OOB), authz/findings/reporting, the AI-workspace/mobile layer +
all unmerged branches, and the control API/MCP registry/full UI. Every finding
below was fixed via TDD in an isolated git worktree, merged into `main`, and
verified with a combined `go build ./... && go vet ./... && go test ./...`
after each merge. See `CHANGELOG.md` `[Unreleased]` for user-facing detail.

| id | severity | area | resolution |
|----|----|----|----|
| A1 | Critical | checkscript/activescript | **FIXED** — `Compile()` never bounded Starlark execution steps (only `Run()` did); a ~90-byte module-scope comprehension could OOM-kill the process. Both `Compile()`s now call `SetMaxExecutionSteps`. |
| A2 | High | launcher | **FIXED** — instance start/stop had zero auth; any local process or loopback-CSRF page could kill/spawn pentest sessions. Added a per-process token (`~/.interseptor/launcher.token`) required on mutating routes, plus bind confirmation before returning success. |
| A3 | High | proxy/intercept | **FIXED** — editing the Host header on a held request changed the wire header but not the actual connection target (confused-deputy/vhost-smuggling). Connection routing now follows the edited Host, on both plain-HTTP and HTTPS-MITM paths. |
| A4 | Medium | mcp/store | **FIXED** — `append_notes` was client-side GET-then-PUT with no atomicity; concurrent agents could silently lose an entry. Now atomic server-side (`Store.AppendNote`, `PATCH /api/notes`). |
| A5 | Medium | report | **FIXED** — the default Markdown findings report didn't sanitize finding text, allowing forged headings/status lines from untrusted content. HTML export was already safe; Markdown now neutralizes line-start structural markers too. |
| A6 | Medium | ios | **FIXED** — no-device manual setup returned a broken `port=0` proxy config in the `.mobileconfig` URL. |
| A7 | Medium | authz | **FIXED** — `authz_check_sessions` reported any 401/403 as "session invalid," including the exact moment a fixed IDOR correctly starts denying access. Now requires real evidence (WWW-Authenticate / login redirect); plain denial is reported separately via `accessDenied`. |
| A8 | Medium | findings | **FIXED** — `createFinding` silently discarded failed PoC flow attachments; now surfaced via a `warnings` array. |
| A9 | Medium | ui/authz.js | **FIXED** — "⧉ From flow" read a `d.requestAuth` field the backend never sent; always failed. Now reads the real `cookie`/`authorization`/`suggestedHeaders` fields. |
| A10 | Medium | ui/scanner.js | **FIXED** — testing a passive check always showed "No finding" (JS only handled the active-check response shape). Now branches on the real shape. |
| A11 | Medium | control/api.go | **FIXED** — `/api/reference` was missing 44 registered routes (autopwn/*, human-input/*, share/*, and more); MCP stdio config snippet hardcoded port 9966 regardless of the actual instance. Both fixed. |
| A12 | Medium | docs | **FIXED** — README claimed 84 MCP tools (actual 83) and still advertised the removed content-discovery feature. |
| A13 | Low | android/ios | **FIXED** — `adbExec`/`simctlExec`/`ideviceExec`/SSH post-dial exec had no timeout; a wedged device command could hang a handler goroutine forever. All now bounded (~30s). |
| A14 | Low | intruder | **FIXED** — `threads<=0` silently clamped to 1 with no signal to the caller; now a clear 400. |
| A15 | Low | humaninput | **FIXED** — unanswered prompts never expired, accumulating forever. Now expire after 1 hour, unblocking any waiter. |
| A16 | Low | tlsca (test) | **FIXED** — `TestLoadOrCreateDirPerms` was a permanent Windows false-positive (NTFS doesn't map POSIX bits). Now skips the POSIX assertion on Windows only. |
| A17 | Low | ui/proxy.js, ui/notes.js | **FIXED** — purge-toast referenced a nonexistent `freedBytes` field; the AI-notes-organize stream bypassed the shared CSRF/401-handling wrapper (not exploitable — the server enforces CSRF independently — but broke the 401→login redirect on session expiry). |
| A18 | Low | proc (Windows) | **DEFERRED** — added `AliveInterseptor(pid)` (image-name-verified, closes a narrow PID-reuse race) but did **not** wire it into `launcher.go`'s kill path: that file has no build tag and can't reference a Windows-only symbol without a cross-platform shim in `internal/proc`'s OS-agnostic entry point, which needs equivalent-but-different logic on Unix (image-name isn't available the same way) — a small but real design decision, deferred rather than rushed. |
| A19 | — | branches | **RESOLVED (housekeeping)** — `feat/ai-workspace-and-backlog`, `feat/autonomous-pentest`, `feat/collab-and-autopilot-fix`, `loop/pm-autonomous`, `loop/ui-and-bugs`, `redesign/ui-overhaul` were all already fully merged into `main` (0 commits ahead, confirmed ancestors) before this audit started. Deleted locally and on origin. |
| A20 | — | discovery | **NOTED, no action** — content-discovery was already fully removed from `main` (commit `5d28f58`) before this audit started; the initial task brief assumed it still needed trimming and was stale by one day. |

**Update (2026-07-08) — the flakiness above is FIXED.** Root cause confirmed:
all 14 built-in active checks race for one shared, mutex-protected request
budget across concurrent goroutines — which check gets to run before the
budget is exhausted is scheduler-dependent, not an ordering/timing quirk.
Both tests now isolate to the single check under test via a new
`onlyCheck()` helper. Verified 30/30 clean under `-count=30`.

Same pass also fixed 3 more pre-existing Linux-CI-only failures that had
gone unnoticed locally (Windows) and unenforced (no test gate on the release
workflow) until this cycle added one: `TestAliveInitProcess` (assumed
permission to signal PID 1 — removed as redundant with
`TestAliveCurrentProcess`), `TestForceReapsChild` (never reaped its killed
child, so the zombie still answered liveness checks as alive), and
`TestBackupToRefusesExisting` (relied on non-portable SQLite driver behavior
instead of an explicit existence check).

Once these 4 stopped masking it, actually running `-race` end-to-end on CI
for the first time caught one more, genuine race: `TestSwitchProjectAcceptsExplicitPath`
synchronized with a bare `time.Sleep` and a plain shared variable instead of
a real happens-before edge. Fixed with a channel. See `CHANGELOG.md`
`[Unreleased]` for detail; all four fixes are pushed and pending final
Linux-CI confirmation.

---

Output of a PM-driven, multi-iteration audit loop (22 iterations across backend
correctness, security, UI/UX, frontend, API contract, performance, test coverage,
accessibility, lifecycle/concurrency, malformed-input, resource/DoS, cross-cutting
concerns, and the proxy forwarding hot path), followed by a backlog burn-down
campaign that resolved every remaining open item.

> **Status: fully burned down.** Every row below is **FIXED** or **DEFERRED with a
> documented rationale**. Fixes are in the tree, tested (`go test ./...`, `go vet`,
> `CGO_ENABLED=0 go build` all green); forwarding-path changes were additionally
> live-verified (`curl -x` HTTP + HTTPS-MITM + keep-alive → 200s, flows captured)
> and the a11y row-keyboard work was live-verified in the preview (Tab focuses a
> flow row, Enter selects it). See [CHANGELOG.md](../CHANGELOG.md). Two items remain
> **deferred by design** (039, 070) — rationale below.

## High (S1) — RESOLVED

| id | area | resolution |
|----|------|------------|
| AUDIT-003 / 020 | proxy | **FIXED** — `io.LimitReader(64 MiB)` + `restoreBody` stream-untransformed fallback on over-cap/read-error in `maybeInterceptResponse`, `dumpRequest`, and `applyBodyRule`. Forwarding path live-verified. |
| AUDIT-054 | sender | **FIXED** — `tryBeginRefresh()`/`endRefresh()` in-progress sentinel on `loginState`; only one goroutine runs the macro (concurrency test added). |
| AUDIT-027 / 048 | a11y | **FIXED** — shared `wireRowKey(el,onActivate)` helper in `core.js` (role=button + tabindex=0 + Enter/Space→activate) applied to flow rows, held-intercept queue, scan items + active-scan findings, rep/intr tabs + history, finding/PoC rows, authz result rows. Live-verified Tab+Enter. (cmdk palette already focus-managed.) |

## Medium (S2) — RESOLVED (12 fixed, 1 deferred)

| id | area | resolution |
|----|------|------------|
| AUDIT-006 | proxy | **FIXED** — `respKeepAlive()` keeps chunked HTTP/1.1 alive. Forwarding path. |
| AUDIT-014 | proxy/sec | **FIXED** (earlier) — Repeater/Intruder/WS refuse own-listener targets (SSRF self-guard); RFC1918 deliberately *not* blanket-blocked. |
| AUDIT-067 | proxy | **FIXED** — `dialViaUpstream` CONNECT-tunnels WS upgrades through the configured upstream; direct path byte-identical. |
| AUDIT-060 | discovery | **FIXED** — local `budgetHit` flag replaces the `ctx` reassignment data race. (Needs a final `go test -race` pass on a cgo-enabled host — race detector unavailable here.) |
| AUDIT-069 | store | **FIXED** — `InsertFlow` row + FTS wrapped in one transaction. |
| AUDIT-040 | perf | **FIXED** — `UpdateFlow` drops the pre-SELECT; one tx updates the row + `flows_fts` columns directly. |
| AUDIT-041 | perf | **FIXED** — `GCBodies` runs in a background goroutine after the purge HTTP ack; UI toast reworded "reclaiming space…". |
| AUDIT-015 | tlsca | **FIXED** — FIFO-bounded leaf-cert cache (`leafCacheMax=2048`). |
| AUDIT-016 | sec | **FIXED** — `re_search` caches compiled regexes (`sync.Map`) + caps input (`reMaxText=256 KiB`). |
| AUDIT-017 | sec | **FIXED** (earlier) — user-supplied regex length-capped (`maxRulePattern=4096`). |
| AUDIT-055 | activescan | **FIXED** — boolean-SQLi check skips tiny baselines (`lb < 64`) to avoid false positives (test added). |
| AUDIT-056 | wsrepeater | **FIXED** — deadline reset (`readFor+5s`) after handshake verification. |
| AUDIT-062 | frontend | **FIXED** — `authzTarget()` resolves the live `state.selId || authzFlowId` at action time; `fillFromFlow`/`checkSessions`/`runBody` consistent (+ `syncAuthzLabel`). |
| AUDIT-063 | frontend | **FIXED** — table sort routes through `renderMap()` so crumb/count/warn stay current. |
| AUDIT-039 | perf | **DEFERRED (by design)** — making host/header substring search index-able means switching to prefix/exact match, a product decision that changes the search UX. Not a correctness bug; revisit if 100k-row scans become a real bottleneck. |

## Low (S3) — RESOLVED (8 fixed, 1 deferred, 1 rejected)

| id | area | resolution |
|----|------|------------|
| AUDIT-018 | sec | **FIXED** — `mcpAuthorized` fails **closed** once any API key is known to exist (`mcpKeysSeen` atomic). |
| AUDIT-028 | frontend | **FIXED** — export/CA download toasts now say "Downloading…" instead of claiming success up front. |
| AUDIT-035 | frontend | **FIXED** — `fmRenderSide` snapshots `state.fm.id` and bails if a newer `flowPopup` superseded it. |
| AUDIT-049 | a11y | **FIXED** — segmented `<button>` toggles set `aria-pressed` across ai/apipanel/flowmodal/notes/map/tools/settings. |
| AUDIT-050 | a11y | **FIXED** — `aria-label`s on the remaining placeholder-only inputs (match/replace, scope host/path, intruder grep/extract/proc, token+login macros, OOB, checkId, asMax, authzMax, keyLabel, note); dark `--fg3` nudged `#8e8e99`→`#9a9aa6` for ~AA contrast. |
| AUDIT-059 | control | **FIXED** — `WSFramed` debounce uses timer-identity (closure compares the stored timer) so a stale `AfterFunc` can't delete the new entry. |
| AUDIT-071 | store | **FIXED** — `ws_frames` trimmed to the most-recent N per flow on insert (`wsFramesPerFlow=5000`). |
| AUDIT-072 | proxy | **FIXED** — `writeResponseHTTP` declares + forwards `resp.Trailer`. Forwarding path. |
| AUDIT-019 | sec | **DEFERRED** — self-update checksum/signature hardening; CLI-only (not HTTP/MCP reachable), low risk. Tracked for a release-engineering pass. |
| AUDIT-070 | store | **DEFERRED (by design)** — no `PRAGMA user_version` framework. The additive-`ALTER` migration loop is idempotent and sufficient today; a version framework is ceremony + regression risk with no current bug. Revisit before the first non-null-no-default column. |
| AUDIT-077 | control | **NOTED** — no per-client SSE cap; loopback-gated by `securityGuard`, matches the threat model. No action. |
| AUDIT-010 | store | **REJECTED** — `UpdateFlow` doesn't change the note column, so reindexing FTS with the existing note is correct; the suggested "pass `f.Note`" would wipe the note from the index. |

## AI-optimization deferred ideas — SHIPPED

- **Report export** — `report.Project()` renders the full engagement report (curated
  findings with status + PoC flows, then a passive-scan appendix) as Markdown.
  Exposed as `GET /api/findings/report`, MCP tool `export_report`, and a
  "⤓ Export report" button in the Findings tab. Tested end-to-end.
- **Activity workflow-grouping** — the Activity feed draws a separator between an
  AI's distinct workflows (intent change, or a >20s time gap), so a multi-step
  sequence reads as one block. CSS-light, no backend change.
- **Auto-CA-trust** — **DECISION: document, do not automate.** Trusting the CA is a
  one-time manual step per client by design (Interseptor never edits the OS trust
  store). The Settings → TLS panel and the `ca_info` MCP tool both carry concise
  per-OS trust steps (macOS / Windows / Linux / Firefox / iOS / Android + curl).

## Rejected during the audit (false positives / overstated) — for the record

- `events.go` broadcast "marshals under the lock" — source marshals **before** the lock (correct).
- `response.go` nil-map "panic" in ForwardResponse/DropResponse — Go nil-map **read+delete** are safe.
- `OpenBody` "Host substring is a bug" — intentional search-box UX (= the deferred perf item AUDIT-039).
- `wsframes.go` 32-bit `make` overflow — `min(ln, wsPreviewMax=512)` bounds it to ≤512.
- a11y "aiPulse has no keydown" — already present at `activity.js`.
- activescan `break` "S1 blocks on semaphore" — the `ctx.Err()` check is before dispatch (negligible cost).
