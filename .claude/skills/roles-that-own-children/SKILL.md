---
name: roles-that-own-children
description: ARIA roles with required or forbidden children in the embedded UI (tablist, listbox/option, main/aside). Use when adding a button inside a History row, a control inside a tab chip, a landmark inside a panel, or when axe reports `nested-interactive`, `aria-required-children` or `landmark-complementary-is-top-level`.
---

# Roles that constrain their children

Three structures in the UI have rules about what they may contain. Each was violated once and each
fix has a source-text contract in `internal/control/ui_a11y_r2_test.go`.

## An `option` has no interactive descendants

History rows are `role="option"` in a listbox. A screen reader presents an option as one item, so a
`<button>`, a `role="button"` or anything with `tabindex` inside it is unreachable or announced
wrongly. `tabindex="-1"` does not make it acceptable: axe flags it (`nested-interactive`), and it
was 220 nodes.

- In-row affordances (the actions glyph, tag chips) are plain elements: `<span>` with `role="img"`
  and a label where a name is needed.
- Their behaviour comes from the row: the delegated click handler on `#rows`, the row's
  `oncontextmenu`, and Shift+F10 on the row for keyboard users. Every in-row mouse action must be in
  that menu.
- Put what the affordance conveyed into the row's accessible name (the row label ends with its tags).
- Do not add per-row listeners. The list is windowed over 10k–100k flows; see `windowed-lists`.

## A `tablist` owns only tabs

The Repeater and Intruder strips hold, per chip, a tab button and a Close button, and end with "+".
`role="tablist"` on the strip made Close and "+" its children (`aria-required-children`).

- The strip is role-less. A separate, visually boxless element with `role="tablist"` owns the tab
  buttons through `aria-owns`, so DOM order, classes and CSS are unchanged.
- Close and "+" are ordinary buttons. Delete on a focused tab closes it, honouring a disabled Close.
- `aria-owns` was verified in Chrome's accessibility tree. WebKit's reordering support is
  incomplete (WebKit bug 223798 was still open when this was written; MDN notes VoiceOver exposes
  `aria-owns` only from macOS 14.3 / iOS 17.3). The desktop app is a WKWebView with a macOS 12
  minimum, so on older systems the tabs may be announced without a tablist. If that has to be
  closed, the alternative is to move Close out of each chip in the DOM, which changes layout and the
  `.rep-tab .rt-close` contracts.

## `aside` inside `main` is not a landmark

A complementary landmark must be top-level. A side rail inside a panel is a labelled group:
`<div role="group" aria-label="…">`, same class and id.

## Checking

axe-core over all 12 panels, both themes, desktop and 390 px: 230 flagged nodes before these fixes,
0 after. Re-run it after touching row, tab-strip or landmark markup; a count is not a pass unless
the run seeded History rows, two findings and at least two Repeater tabs. Compare screenshots
before and after: an accessibility fix should not move a pixel unless it says so.
