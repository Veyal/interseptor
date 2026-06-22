# Changelog

All notable changes to the Conduit design project are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
each "release" is an iteration of the Conduit design (`Conduit.dc.html`).

## [Unreleased]

### Fixed
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
