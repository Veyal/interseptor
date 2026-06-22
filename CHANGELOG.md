# Changelog

All notable changes to the Conduit design project are recorded here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
each "release" is an iteration of the Conduit design (`Conduit.dc.html`).

## [Unreleased]

### Added
- Design spec for slice #1 (core intercepting proxy): `docs/superpowers/specs/2026-06-22-interceptor-proxy-core-design.md`. Stack: Go core (single static binary) + React web UI; persistent-lean storage (SQLite metadata + on-disk bodies); proxy listener configurable at runtime (default `127.0.0.1:8080`); control plane on `127.0.0.1:9966`.
- Implementation plan for the foundation slice (store + capture + HTTP forward proxy + runnable binary), built bottom-up with TDD: `docs/superpowers/plans/2026-06-22-interceptor-foundation.md`.

### Changed
- Product renamed from "Conduit" to **Interceptor** (existing references will be updated during the slice-1 build).

## [2026-06-22] — Project setup

### Added
- Imported the Conduit design specification (intercepting HTTP proxy / HTTP client UI) from the source archive: `Conduit.dc.html`, the `support.js` runtime, and `screenshots/`.
- `CLAUDE.md` documenting the Design Component architecture, the `renderVals()` render-derived-view-model pattern, the `<sc-for>` / `<sc-if>` / `{{ }}` template DSL, and the six product modules.
- `CHANGELOG.md` (this file) plus a changelog-update policy in `CLAUDE.md`.
- `Stop` hook (`.claude/hooks/changelog-reminder.sh`, wired via `.claude/settings.json`) that reminds Claude to update this changelog when project files change without a matching entry.
- Initialized the git repository (`main`) and added `.gitignore`.
