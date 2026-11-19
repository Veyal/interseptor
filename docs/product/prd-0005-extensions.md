# PRD-0005 — Extension / plugin API

*Owner: Product · Status: ✅ Shipped (Phases 1–3) · Priority: Shipped · Last updated: 2026-11-18*
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

## 4. Delivery phases

1. **Hooks (Go, Phase 1)** — `internal/plugin` registry: `OnFlowCaptured`, `OnScanIssue` (in-process,
   same binary; packages register at init). Used by first-party features and the Copy Abilities hook bridge.
2. **UI slots (Phase 2)** — landed as **Copy Abilities** (`internal/abilities`, **Configure → Abilities**): declared sidebar menus/panels, flow inspector tabs, context-menu actions, and command-palette commands via ES modules (`mount(container, ctx)` / `unmount(container)`).
3. **Sandboxed Starlark host (Phase 3)** — landed as **Copy Abilities** (`hooks/*.star`): SHA-256 quarantined-until-approved `flow.captured` hooks (`on_flow(flow)`) and on-demand/MCP actions (`run(ctx)`).
4. **Docs + examples** — [`docs/extensions.md`](../extensions.md), [`docs/abilities.md`](../abilities.md), [`examples/extensions/hello/`](../../examples/extensions/hello), and [`examples/abilities/`](../../examples/abilities/).

## 5. Acceptance (issue #25)

- [x] PRD (this doc)
- [x] Phase 1 prototype extension — `internal/plugin/annotator` (flow annotator, tested), opt-in via `INTERSEPTOR_EXT_ANNOTATE_HOSTS`.
- [x] Phase 1 docs for authors — [`docs/extensions.md`](../extensions.md) + [`examples/extensions/hello/`](../../examples/extensions/hello).
- [x] Phases 2 & 3 (**Copy Abilities**) — `internal/abilities`, **Configure → Abilities** UI, REST/SSE/MCP/CLI surfaces, SHA-256 quarantine trust model, [`docs/abilities.md`](../abilities.md), and example abilities under [`examples/abilities/`](../../examples/abilities/).
