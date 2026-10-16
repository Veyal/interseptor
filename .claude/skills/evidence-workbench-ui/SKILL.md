---
name: evidence-workbench-ui
description: Conventions for extending Interseptor's evidence workbench UI (the Flight Bar, flow drawer, split panes, sheets, palette, dock) without breaking its shared accessibility, motion, privacy and forwarding contracts.
---

# Evidence workbench UI conventions

Use this when adding or changing anything in `internal/control/ui/` that touches the shell, a panel's list/detail layout, evidence attachment, shortcuts or the phone navigation. `docs/architecture.md` (Web UI) names the modules; `ui_a11y_audit_test.go` enforces the rules below across every file.

## Render server state, never derive it

- Readiness (`ready`, `stage`, `gaps`, `checks`) and project blockers come from the server (`readiness.go`, `GET /api/project/readiness`). The client displays them and never recomputes pass or fail. Unknown gap codes render generically.
- A failed segment of `projectState` goes stale on its own; do not let one failing endpoint blank the Flight Bar.
- Do not touch `internal/proxy` or capture code for UI work. Fetch bodies lazily, truncate above 1 MB with a visible notice, and throttle live counters (`flow.new` refreshes the bar at most every 2 s).

## Controls and icons

- Icons come from the sprite (`icon('name')` in `core.js` or `<use href="#i-name">`). Do not type `⧉ ◎ ▦ ＋ ◧ ▾ ✕` into markup or scripts; the audit fails on them. CSS `content:` disclosure triangles are the only exception.
- Every button and field has a task-specific accessible name; icon-only controls carry `aria-label`. No inline `style=` in new markup or modules; use the `.u-*` utilities and tokens.
- Status is icon plus text (plus a pattern for meters), never colour alone. Meters use `role=meter` with `aria-valuetext`.
- Targets are at least 24px, 44px under `@media (pointer:coarse)`; new interactives get a `:focus-visible` ring. Programmatic-focus containers (`tabindex=-1`) are the only elements that may drop the outline.

## The Flight Bar

- `#bar` is the only top chrome: one `<header>`, `--topbar-h` tall (42px; 36 compact, 50 comfortable, 54 touch), and `--sticky-top` is just `--topbar-h`. There is no second strip. Add a control to a zone in `index.html` and give it a tier in `shell.css`; do not stack a row under the bar.
- Zones: brand and engagement pills (project, scope) on the left, the `#cmdkBtn` omnibar centred between two equal zones, status (intercept state, identity, the readiness ring, the live chip, the theme toggle) on the right. The omnibar, scope pill, ring and live chip are never `display:none` at any width (`TestUIFlightBarNeverHidesAnAffordance`); narrow tiers drop decoration (brand text, key labels, the shortcut hint, the logo) or pieces another surface covers (the dock More sheet, the palette `@`).
- A secondary indicator that used to be a permanent chip belongs in the readiness popover (`#ctxRingPop`), not on the bar. The popover is non-modal: Escape (or Close), an outside click and tabbing out close it; Escape and Close return focus to the ring, and a row that leaves for another surface parks focus on the ring rather than on a control it just hid. Rebuild its checklist only when the content changed, and hand focus to the first control left if it did, or a live refresh drops the keyboard focus.
- Readiness is report-time: the ready fraction, arc and blocker count show only while the Report view is open (`shouldShowReadinessSignals`); outside it the ring flags only missing setup, with a shape. State rules and accessible names live in the pure `ctxbar-model.js`, and a visible ring text must appear in its accessible name.
- Children of a capped, scrolling flex column (the checklist) need `flex:none`, or multi-line rows shrink and overlap on a phone. Give the scrolling list padding (undone by a negative margin) so a Fix button's focus ring is not clipped, and under 560px of window height let the list grow to its content and the popover itself scroll (a list that must share 70vh with a fixed header and footer collapses).
- `#bar` is `white-space:nowrap`, and a popover inside it inherits that: reset `white-space:normal` on any popover you add there (the Connection popover's long addresses spilled until it did).
- Text another module writes into the bar can be too long for it (`app.js` writes `· REQ intercept ON · RESP intercept ON · 2 held` into `#icptStat`). Do not hide it: keep the original for assistive technology (clipped, never `display:none`) and show a short aria-hidden twin that `ctxbar.js` keeps in step (`#icptCompact`).
- `projectState` notifies synchronously, so a subscriber that changes the store (restoring the saved identity) re-enters itself: set its once-flag before the call and render `projectState.get()`, not the snapshot it was handed (the stale snapshot repainted "No identity" over the restored one).
- The bar clips vertically (sideways scrolling is its last resort), so a control plus 4px of focus ring each side must fit `--topbar-h` at every density (`TestUIFlightBarHeightTokens`); set a taller control's own padding, not the bar.

## Motion and live regions

- Only `transform` and `opacity` animate or transition in any stylesheet added after `app.css`. The global reduced-motion block in `app.css` zeroes every animation and transition; never force an animation with `!important`.
- Polite live regions are rate limited (blocker count at most once per 5 s). Assertive regions are for errors only.

## Shortcuts

- Register new single-key bindings through `keys.js` (`registry.register({id, keys, scope, run, label, group})`). A duplicate key in the same scope throws at registration; the audit also greps registrations.
- Single letters are disabled in editable controls and open dialogs and follow the "Single-key shortcuts" Settings switch (WCAG 2.1.4). Every key has a visible button. Existing handlers in `app.js`, `proxy.js` and `tools.js` are listed in `shortcuts-table.js`; add new legacy-style bindings there.

## Reversibility

- A replaced dialog keeps its id in `MODAL_IDS` and an action in the command palette (`LEGACY_MODALS` in `cmdk-logic.js`) until a dedicated cleanup commit removes it, after its replacement is verified in a real browser.
- Preserved ids (`tabs`, `crumb`, `mobileToolSelect`, `sseStatus`, `proxyAddr`, `controlAddr`, `deviceProxyChip`, `findReadinessBoard`, `cmdkBtn`, `setNav`, `bar`, `ctxScope`, `ctxRing`) and every `data-tab` stay; the palette and the engagement sheet click `ctxScope`, and the startup lock makes `bar` inert.
- The locked state is the separate `login.html` page: no project name, target, counts or API calls to project data, and the six-digit PIN flow stays as is.

## Verification

Pure logic lives in `*-model.js` or dependency-free modules and is tested with `node --test` under `internal/control/ui/_js-tests` (not embedded). Static contracts go in a `ui_<area>_test.go`. Real-device behaviour (iOS keyboard and `dvh`, Android back) cannot be asserted statically; list it as a manual check.
