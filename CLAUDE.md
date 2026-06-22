# CLAUDE.md

Guidance for Claude Code (and other agents) working in this repository.

## What this repository is

**Interceptor** — a lightweight intercepting HTTP/HTTPS proxy + security-testing toolkit (a
leaner Burp Suite), implemented as a single static **Go** binary that runs a MITM proxy plus an
embedded web UI. See [README.md](README.md) for the product overview and
[CONTRIBUTING.md](CONTRIBUTING.md) for the **code standards you must follow**.

> History note: this project began as a UI design spec (`Conduit.dc.html`, a Design Component).
> That mock UI has been removed — the real product is the Go app described below. The original
> design remains in git history if ever needed.

## Build, run, test

```bash
go run ./cmd/interceptor          # proxy on :8080, control UI/API on :9966
CGO_ENABLED=0 go build ./cmd/interceptor   # single static binary (no cgo)
go test ./...                     # all tests
go test -race ./...               # race detector (must be clean)
go vet ./...                      # static checks (must be clean)
```

`INTERCEPTOR_NO_BROWSER=1` suppresses the browser auto-open. Runtime data: `~/.interceptor/`.

## Architecture (where things live)

One binary, two localhost listeners (proxy `:8080`, control `:9966`). Single-responsibility
`internal/*` packages wired by `cmd/interceptor`; the package table is in
[README.md](README.md#architecture). Key seams:

- **`internal/store`** — SQLite metadata + content-addressed body files. Bodies stream to disk
  (`io.TeeReader`), never buffered whole. Sent requests (Repeater/Intruder) are flows tagged with
  a flag and filtered via `QueryFlowsFilter`.
- **`internal/proxy`** — forward proxy + `CONNECT`/TLS MITM + WebSocket frame relay. Capture is
  best-effort: it never breaks forwarding. The intercept gate + match-&-replace run before forward.
- **`internal/control`** — REST + SSE API and the embedded UI. Routes are registered in `routes()`;
  JSON DTOs are kept separate from `store` structs; live changes broadcast over SSE.
- **`internal/control/ui/index.html`** — the entire UI: one self-contained file (vanilla JS,
  `//go:embed`). Theme via CSS variables (never hardcode hex); `esc()` interpolated values.

When adding a module: add its state/queries to `store`, its logic to a focused `internal/*`
package (TDD), expose it through `control` (REST + SSE), and add a UI tab. Follow the existing
modules (`sender`/`intruder`/`scanner`) as templates.

## Conventions (summary — full detail in CONTRIBUTING.md)

- **TDD**, no cgo, `go test`/`-race`/`vet` green before every commit.
- **Conventional Commits** (`feat(scope): …`), each ending with the
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer.
- Guard shared state with a mutex; fire notifier callbacks outside the lock (they may run
  concurrently and must be thread-safe).

## Changelog policy

Every change is recorded in [CHANGELOG.md](CHANGELOG.md) ([Keep a Changelog](https://keepachangelog.com/)).
**Before finishing any turn in which you modified code, docs, or config**, add a bullet under
`## [Unreleased]` (grouped `Added`/`Changed`/`Fixed`/`Removed`). When a batch is finalized, rename
`[Unreleased]` to a dated section and start a fresh empty `[Unreleased]`.

A `Stop` hook (`.claude/hooks/changelog-reminder.sh`, wired in `.claude/settings.json`) reminds you
if project files changed without a matching `CHANGELOG.md` update. It is a non-destructive nudge.
