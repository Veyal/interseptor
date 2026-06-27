# Interceptor — Audit Backlog (CLEARED)

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
  one-time manual step per client by design (Interceptor never edits the OS trust
  store). The Settings → TLS panel and the `ca_info` MCP tool both carry concise
  per-OS trust steps (macOS / Windows / Linux / Firefox / iOS / Android + curl).

## Rejected during the audit (false positives / overstated) — for the record

- `events.go` broadcast "marshals under the lock" — source marshals **before** the lock (correct).
- `response.go` nil-map "panic" in ForwardResponse/DropResponse — Go nil-map **read+delete** are safe.
- `OpenBody` "Host substring is a bug" — intentional search-box UX (= the deferred perf item AUDIT-039).
- `wsframes.go` 32-bit `make` overflow — `min(ln, wsPreviewMax=512)` bounds it to ≤512.
- a11y "aiPulse has no keydown" — already present at `activity.js`.
- activescan `break` "S1 blocks on semaphore" — the `ctx.Err()` check is before dispatch (negligible cost).
