# PRD-0005 — Extension / plugin API

*Owner: Product · Status: 📝 Draft · Priority: Later · Last updated: 2026-07-19*
*Links: [strategy.md](strategy.md) · [roadmap.md](roadmap.md) · [#25](https://github.com/Veyal/interseptor/issues/25)*

## 1. Summary

Ship a **stable, versioned extension surface** so third parties can add UI panels,
checks, and lifecycle hooks without forking Interseptor — the long-term ecosystem
play beyond Starlark checks and rule packs.

## 2. Problem

Custom Starlark checks + packs cover detection content. They do **not** cover new
UI tabs, alternate history views, or process hooks (e.g. on-flow-captured). Burp’s
extender moat is partly “you can add a panel.” We need a path that stays
offline-first, sandboxed, and no-cgo.

## 3. Goals / Non-goals

**Goals**
- Load model: explicit install (dir or pack), never silent download.
- Sandbox: no arbitrary native code in v1; prefer Starlark / WASM / ES modules for UI.
- Versioned host API (`interseptor.ext` semver) with capability flags.
- One official example extension (hello panel or flow annotator).

**Non-goals (v1)**
- Full Burp Extender Java API parity.
- Marketplace / auto-update of untrusted binaries.
- Hot-path hooks that can break forwarding (capture stays best-effort).

## 4. Proposed phases

1. **Hooks (Go)** — `internal/plugin` registry: `OnFlowCaptured`, `OnScanIssue` (in-process,
   same binary; packages register at init). Used by first-party features first.
2. **UI slots** — declared panel id + ES module path under `~/.interseptor/extensions/`.
3. **WASM / Starlark host** — untrusted third-party logic.
4. **Docs + example** — `examples/extensions/hello/`.

## 5. Acceptance (issue #25)

- [x] PRD (this doc)
- [ ] Prototype one official extension
- [ ] Docs for authors

Phase 1 stub lands in `internal/plugin` so later work has a package home.
