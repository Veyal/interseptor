# Interseptor — Roadmap

*Owner: Product · Last updated: 2026-07-16 · Horizon: rolling. Now/Next/Later, not dates.*

Strategy: a proxy operated by **a penetration tester and their AI assistant together**.
Priority order: **(A) engagement close-out**, **(B) trustworthy AI**, **(C) table-stakes
proxy depth**, then **(D) reach / packaging**. See [improvements.md](improvements.md).

## Themes

1. **Close the engagement** — findings → evidence → report is the sticky loop.
2. **AI-operable + trustworthy** — agents drive the same engine; humans see *why* Autopilot filed or rejected.
3. **Project = whole workspace** — tabs, presets, packs, notes travel with the project.
4. **Trustworthy core** — HTTP/2, correctness, never break forwarding.
5. **Interop & reach** — install paths, packs ecosystem, collaboration.

## ✅ Shipped (through v1.5.0)

MCP (tool registry), scope, Repeater/Intruder, scanner + active scan + Autopilot, findings
redesign, rule packs + check CLI + Starlark stdlib, project-scoped + project-DB UI tabs,
Intruder Numbers / AI payloads / result viewer, retention, OpenAPI, mobile helpers,
remote/tunnel pieces, HAR + full project export.

## Now (committed slice)

| Item | Why | Status |
|---|---|---|
| Engagement close-out checklist | Convert “cool proxy” → “I finish work here” | Docs + Findings UX |
| Project-DB UI state (Repeater/Intruder/presets) | Drafts survive machine/browser switches | Shipped this cycle |
| Official rule packs + Checks UI install | Growth channel without growing core | Shipped this cycle |
| Intruder Interesting filter → Finding | Analysis, not firehose | Shipped this cycle |
| Autopilot Trust ledger copy | Surface verifier gates | Shipped this cycle |
| MCP cookbook v2 recipes | Agent onboarding | Shipped this cycle |
| Packaging truth (tool counts, install docs) | Stop lying in README | Shipped this cycle |

## Next

| Item | Theme | Effort | Issue |
|---|---|---|---|
| HTTP/2 MITM support | Trustworthy core | L | [#19](https://github.com/Veyal/interseptor/issues/19) |
| Content discovery (scope-aware soft-404) return | Table stakes | L | [#22](https://github.com/Veyal/interseptor/issues/22) |
| Signed rule packs (minisign) | Packs ecosystem | M | [#24](https://github.com/Veyal/interseptor/issues/24) |
| Message codecs (project-scoped decrypt/encrypt) | Differentiator | Shipped in 1.5.3 | [#28](https://github.com/Veyal/interseptor/issues/28) |

## Later

| Item | Theme | Effort | Issue |
|---|---|---|---|
| Extension / plugin API | Differentiator (Burp moat) | XL | [#25](https://github.com/Veyal/interseptor/issues/25) |
| HTTP/3 / QUIC | Forward-looking | XL |
| Team roles / audit log / commercial packaging | Commercial | XL |
| WebSocket via upstream proxy | Interop | M |

## Prioritization model

RICE — Reach × Impact × Confidence ÷ Effort. Revisit each planning cycle.
“Now” is a small committed slice; “Next” / “Later” are intentionally undated.

## How we work

1. Roadmap item → GitHub issue (and PRD when L/XL).
2. TDD + CHANGELOG under `[Unreleased]`.
3. Measure against engagement outcomes: report exports, findings filed, Autopilot accept rate.
