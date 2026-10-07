---
name: collmatrix
description: Rules for internal/collmatrix (identity matrix, coverage, example diff, Intruder handoff, timing, run evidence) and its UI (js/collections-matrix.js).
---

# collmatrix

- The package never imports `control` or `mcp`. The control layer mounts `Service.Routes(Callers)` into `collRoutes` and adapts `Service.Tools()` into `internal/mcp/collections.go`. Add a route or tool there only, with a catalogue description.
- Every send goes through `collrun.Runner` with an `identityBackend` wrapper; there is no second egress. The wrapper edits the StepInput copy (headers, `Auth` set to noauth), never the stored item, and appends `identity:<name>` to `Applied`. Do not add a send path here.
- Identity credentials live only in `Identity.Headers` (`json:"-"`). Anything that leaves the package passes through `Service.scrub` (the backend's single scrubber). Canary tests pin the matrix, the diff and the handoff.
- Matrix runs force scope policy `block`, persist `discard`, iterations 1. Handlers ignore a caller's `scopePolicy`. Tool calls are AI calls (`SourceMCP`, `AI`): no secrets in a handoff, ever. Handlers only honour `includeSecrets` when `Callers.IsAI` says human.
- Without `Callers.IsAI` every caller is treated as AI. That is the safe default.
- Flags are hypotheses (`violation`, `unexpected_denial`), never findings: copy the "reproduce before reporting" wording when you add outputs.
- Coverage reads spec operations from the item sidecar (`openapi.path|method|operationId`) and stored runs only; interactive sends are not runs and are not counted.
- The sender records total round trip only. Timing outputs carry `TimingNote`; never present a DNS/connect/TLS split.
- Bounds: 12 identities, 200 rows, 20 remembered matrices, 60 attachments per call, 200 JSON changes, 512 KiB bodies, 1 MiB request bodies.
- UI: `collections-matrix.js` is a self-contained module (injects `css/collections-matrix.css` itself, imports only shared modules, never `collections.js`). It never touches `index.html`; it self-registers five `registerCommand` entries on import (identity matrix, coverage, timing, example diff, Intruder handoff), each opening an `openSheet` sheet. **The integration/merge step must add `<link rel="stylesheet" href="/css/collections-matrix.css">` near `collections.css` and `<script type="module" src="/js/collections-matrix.js"></script>` near `collections.js` in `index.html`** — verified locally with a temporary edit (reverted before commit), real boot smoke and screenshots, then reverted so this package's diff stays scoped to new files plus two append-only lines.
- Its pure model is `collections-matrix-model.js` (node-tested in `_js-tests/`). Server strings only via `textContent`.
- `ui_icons_test.go` bans glyph/emoji characters repo-wide, including inside `.js` string literals — matrix cell glyphs are plain ASCII (`=`, `~`, `V`, `?`, `!`, `·`), never arrows or pictographs.
- `ui_primitives_test.go` (`TestUICoreModalIDsIncludeOverlaySurfaces`) requires every `openSheet({id: '...'})` anywhere in `ui/js/*.js` to be listed in `core.js`'s `MODAL_IDS`, so `collMatrixSheet`, `collCoverageSheet`, `collTimingSheet` and `collExampleDiffSheet` were appended there (two-line, additive change to `core.js` — the one allowed exception to "new files only", because it is a repo-enforced invariant, not a feature edit).
