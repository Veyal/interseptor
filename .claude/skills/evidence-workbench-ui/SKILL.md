---
name: evidence-workbench-ui
description: Conventions for extending Interseptor's evidence workbench UI (engagement strip, flow drawer, split panes, sheets, palette, dock) without breaking its shared accessibility, motion, privacy and forwarding contracts.
---

# Evidence workbench UI conventions

Use this when adding or changing anything in `internal/control/ui/` that touches the shell, a panel's list/detail layout, evidence attachment, shortcuts or the phone navigation. `docs/architecture.md` (Web UI) names the modules; `ui_a11y_audit_test.go` enforces the rules below across every file.

## Render server state, never derive it

- Readiness (`ready`, `stage`, `gaps`, `checks`) and project blockers come from the server (`readiness.go`, `GET /api/project/readiness`). The client displays them and never recomputes pass or fail. Unknown gap codes render generically.
- A failed segment of `projectState` goes stale on its own; do not let one failing endpoint blank the strip.
- Do not touch `internal/proxy` or capture code for UI work. Fetch bodies lazily, truncate above 1 MB with a visible notice, and throttle live counters (`flow.new` refreshes the strip at most every 2 s).

## Controls and icons

- Icons come from the sprite (`icon('name')` in `core.js` or `<use href="#i-name">`). Do not type `⧉ ◎ ▦ ＋ ◧ ▾ ✕` into markup or scripts; the audit fails on them. CSS `content:` disclosure triangles are the only exception.
- Every button and field has a task-specific accessible name; icon-only controls carry `aria-label`. No inline `style=` in new markup or modules; use the `.u-*` utilities and tokens.
- Status is icon plus text (plus a pattern for meters), never colour alone. Meters use `role=meter` with `aria-valuetext`.
- Targets are at least 24px, 44px under `@media (pointer:coarse)`; new interactives get a `:focus-visible` ring. Programmatic-focus containers (`tabindex=-1`) are the only elements that may drop the outline.

## Motion and live regions

- Only `transform` and `opacity` animate or transition in any stylesheet added after `app.css`. The global reduced-motion block in `app.css` zeroes every animation and transition; never force an animation with `!important`.
- Polite live regions are rate limited (blocker count at most once per 5 s). Assertive regions are for errors only.

## Shortcuts

- Register new single-key bindings through `keys.js` (`registry.register({id, keys, scope, run, label, group})`). A duplicate key in the same scope throws at registration; the audit also greps registrations.
- Single letters are disabled in editable controls and open dialogs and follow the "Single-key shortcuts" Settings switch (WCAG 2.1.4). Every key has a visible button. Existing handlers in `app.js`, `proxy.js` and `tools.js` are listed in `shortcuts-table.js`; add new legacy-style bindings there.

## Reversibility

- A replaced dialog keeps its id in `MODAL_IDS` and an action in the command palette (`LEGACY_MODALS` in `cmdk-logic.js`) until a dedicated cleanup commit removes it, after its replacement is verified in a real browser.
- Preserved ids (`tabs`, `crumb`, `mobileToolSelect`, `sseStatus`, `proxyAddr`, `controlAddr`, `deviceProxyChip`, `findReadinessBoard`, `cmdkBtn`, `setNav`) and every `data-tab` stay.
- The locked state is the separate `login.html` page: no project name, target, counts or API calls to project data, and the six-digit PIN flow stays as is.

## Verification

Pure logic lives in `*-model.js` or dependency-free modules and is tested with `node --test` under `internal/control/ui/_js-tests` (not embedded). Static contracts go in a `ui_<area>_test.go`. Real-device behaviour (iOS keyboard and `dvh`, Android back) cannot be asserted statically; list it as a manual check.
