# Interseptor — Agent Rules

Single source of truth (symlinked: `CLAUDE.md`, `.cursorrules`, `.opencode/rules.md`).

## Project

HTTP/HTTPS intercepting proxy + security toolkit. Single static Go binary (no cgo). MITM proxy `:8080` + control UI/API `:9966`. See [README.md](README.md).

## Build & Test

```bash
go run ./cmd/interseptor                    # run
CGO_ENABLED=0 go build ./cmd/interseptor    # build
go test ./...                               # test
go test -race ./...                         # race check (must be clean)
go vet ./...                                # static analysis (must be clean)
```

All three checks must pass before every commit.

## Architecture

`internal/*` packages, each one responsibility, wired by `cmd/interseptor`. Key packages: `store` (SQLite + content-addressed bodies), `proxy` (forward + MITM), `control` (REST + SSE + embedded UI), `sender`, `intruder`, `scanner`, `ios`, `android`, `mcp`, `scope`.

UI: `internal/control/ui/` — embedded via `//go:embed`, no build step. See [the Web UI architecture](docs/architecture.md#web-ui) for the current module layout and shared primitives.

Adding a feature: `store` → `internal/*` (TDD) → `control` (REST+SSE) → `js/<feature>.js`.

## Rules

1. **TDD** — failing test first, then code. Every package has `_test.go` coverage.
2. **No cgo** — pure-Go only (`modernc.org/sqlite`, not `mattn/go-sqlite3`).
3. **Never break forwarding** — capture is best-effort, off the hot path.
4. **Small functions, single purpose** — guard shared state with `sync.Mutex`, invoke notifiers outside the lock.
5. **No import cycles** — control talks to packages via interfaces, never reverse.
6. **Bodies stream to disk** — `io.TeeReader`, content-addressed, never buffered in memory.
7. **Conventional Commits** — `type(scope): summary`. One logical change per commit.
8. **Co-Authored-By trailer** — `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`
9. **CHANGELOG.md** — add entry under `[Unreleased]` for every code/doc/config change.
10. **Learn** — when you discover patterns, gotchas, or conventions not captured here, add a skill (`.claude/skills/<name>/SKILL.md`) so future sessions benefit.
11. **No private data in repo** — never commit real request/response data, user history, or any personal/target information. Use only generic example domains (e.g. `example.com`) in tests, docs, and sample data.

## Cursor Cloud specific instructions

The Cloud Agent base image ships Go 1.22. This module requires Go 1.25 (`go.mod`). Leave `GOTOOLCHAIN=auto` (the default). The first `go` command in this repository downloads the 1.25 toolchain into the module cache. `go mod download` is the environment install step and is safe to repeat. Do not install a second Go toolchain.

`gcc` is already installed and is required for `go test -race ./...`.

To exercise the running app without a browser launch or a GitHub update check:

```bash
INTERSEPTOR_NO_UPDATE_CHECK=1 INTERSEPTOR_NO_BROWSER=1 go run ./cmd/interseptor --data-dir /tmp/interseptor-data --project demo
```

The proxy listens on `127.0.0.1:8080` and the control UI/API on `http://127.0.0.1:9966`. Loopback API calls do not need an API key. Ordinary `go test` skips the live GitHub release check unless `INTERSEPTOR_LIVE_RELEASE_CHECK=1`.
