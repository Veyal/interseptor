# Contributing & Code Standards

These are the standards every change to this repository must follow — by humans and by AI
assistants alike. They describe how the existing code is written; match it.

## Golden rules

1. **TDD.** Write a failing test first, watch it fail, then write the minimal code to pass.
   Every package ships `_test.go` coverage. Bug fixes start with a test that reproduces the bug.
2. **Green before commit.** `go test ./...`, `go test -race ./...`, and `go vet ./...` must all
   pass. Concurrency-bearing packages (`proxy`, `intercept`, `intruder`, `control`) must be
   race-clean.
3. **No cgo, single static binary.** `CGO_ENABLED=0 go build ./cmd/interceptor` must succeed.
   Use pure-Go dependencies only (e.g. `modernc.org/sqlite`, never `mattn/go-sqlite3`).
4. **A CHANGELOG entry per change.** Add a bullet under `## [Unreleased]` in
   [CHANGELOG.md](CHANGELOG.md) (Keep a Changelog: `Added`/`Changed`/`Fixed`/`Removed`).
5. **Don't break forwarding to capture.** Capture/analysis is best-effort and off the hot path:
   a body-store or scan error marks the flow and continues; it never fails the proxied request.

## Go style

- `gofmt`/`goimports` clean (tabs, standard ordering). `go vet` clean.
- Exported identifiers have doc comments that start with the identifier name.
- Return errors up the stack; wrap with context (`fmt.Errorf("…: %w", err)`); don't `panic` in
  library code. In HTTP handlers, surface structured errors via the `httpErr` helper.
- Keep functions small and single-purpose; prefer pure helpers (formatting, parsing) that are
  trivially testable.
- Match the surrounding code's naming and idioms over personal preference.

## Architecture & package conventions

- `internal/*` packages each own **one** responsibility and depend downward only
  (see the table in [README.md](README.md)); `cmd/interceptor` does the wiring. Don't create
  import cycles — the control plane talks to other packages through small interfaces
  (e.g. `proxy.Events`), never the reverse.
- **Storage:** bodies stream to disk via `io.TeeReader` and are content-addressed/deduped —
  never buffer a whole body in memory. Metadata goes in SQLite; bodies never do.
- **Persisted requests** (Repeater/Intruder) are stored as flows tagged with a flag
  (`FlagRepeater`/`FlagIntruder`) and filtered via `QueryFlowsFilter`; reuse that rather than
  adding parallel tables for request/response data.
- **Concurrency:** guard shared state with a `sync.Mutex`. Invoke user callbacks/notifiers
  *outside* the lock (so they can't re-enter and deadlock) — which means a notifier may fire
  concurrently and must be thread-safe.

## Control API & UI

- REST handlers live in `internal/control`; register routes in `routes()` and keep JSON DTOs
  separate from `store` structs. Push live changes over SSE (`broadcast`), one event type per
  concern (`flow.new`, `intruder.update`, …).
- The UI is **one** self-contained `internal/control/ui/index.html` (vanilla JS, embedded via
  `//go:embed`) — no build step, no external runtime dependency.
  - Theme via CSS custom properties (`--bg`, `--fg`, `--accent`, …); **never hardcode hex colors**.
  - `esc()` every value interpolated into HTML. Keep dark-mode contrast at WCAG AA.

## Commits

- **Conventional Commits:** `type(scope): summary` — `feat` / `fix` / `refactor` / `test` /
  `docs` / `chore`. Scope is the package or module (`feat(intruder): …`).
- One logical change per commit; keep the tree green at every commit.
- End each commit message with the trailer:

  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  ```

## Before opening a PR

- [ ] `go test ./...` and `go test -race ./...` pass
- [ ] `go vet ./...` is clean
- [ ] `CGO_ENABLED=0 go build ./cmd/interceptor` succeeds
- [ ] New behavior is covered by a test
- [ ] `CHANGELOG.md` updated under `[Unreleased]`
