---
name: windowed-lists
description: Put a long selectable list in Interseptor's embedded UI on the shared windowed renderer (core.js createListView through js/windowed-list.js) and keep selection, keyboard navigation, focus, grouped headers and context menus correct while most rows are not in the DOM.
---

# Windowed lists

Use this when a list can reach thousands of rows (Findings, Scanner issues,
Intruder results) or when you touch how those lists render, select or navigate.

- One window renderer: `createListView` in `core.js` (pure math in
  `virtual-list-model.js`). Never edit it for a feature. Wrap it with
  `createWindowedList` from `js/windowed-list.js`, which adds what a selectable
  list needs; keyboard, reveal and pinned-header math is pure in
  `js/list-rows-model.js` (`buildRows`, `stepIndex`, `revealScrollTop`,
  `stickyState`).
- The container itself must be the scroller (`overflow:auto`, bounded height,
  `flex:1;min-height:0`). If a wrapper scrolled before, move the scrolling to the
  list and leave the chips or toolbar above it.
- Rows are fixed-height only while virtualized: set the explicit height under
  `.vlist` (the class `createListView` adds), so a short list keeps natural rows,
  wrapped titles included. The glue measures the first mounted virtual row once;
  nothing may read layout on the scroll path (#105).
- Grouped lists fold the headers into the one flat row list as rows of the same
  height. The current header is one `aria-hidden` element inside the container,
  before the spacer, drawn from `stickyState`; `position:sticky` on headers inside
  the translated window cannot do it, because the header unmounts as it scrolls.
- Cards of varying height cannot be windowed by a fixed-row renderer. Bound them
  with a page and a Show more control (Scanner affected targets), delegate their
  actions, and keep whole-group actions (Promote, Copy) on the data, not the DOM.
- Delegate every handler from the container (`on:{click,contextmenu,keydown}`).
  A `contextmenu` handler must `preventDefault()` before it opens its menu, or
  core.js's document-level dispatcher opens the workspace menu on top.
- Before painting your own empty or error markup into the container call
  `suspend()`; `render()` resumes. `createListView` cannot tell "owner painted
  something" from "short list", so a resize or scroll would repaint stale rows.
- A hidden pane measures 0px. The glue keeps the window it had, so before
  focusing a row in a list that was just shown again, `refresh()` first
  (`focusFindingRow`).
- Roving tab stop: `renderRow` marks the open row `tabindex="0"`. When that row is
  not mounted the glue gives the stop to the row that has focus (a rebuilt row comes
  back at -1), else to the first row in the viewport, so there is always exactly one.
  The container is `tabindex="-1"` with a `:focus-visible` ring as the fallback that
  `onContainerKey` drives. A focused row that scrolls out parks focus on the container
  instead of `<body>`; focus on another control inside the list (an empty state's
  Clear filters) that rows then replace goes to the open row.
- Row focus rings are drawn inside the row (`outline-offset:-3px`, id-qualified to beat
  app.css's (1,1,0) ring): a key move leaves the row flush against the scroller's edge,
  which clips an outside ring.
- Keys move the selection, so reveal the target row (`reveal(row,{align,inset})`,
  inset = the pinned header's height) before `render()`, then focus the new row
  synchronously: a key repeating faster than a timer lands on the rebuilt old row.
  Mark a clicked or arrowed row as already revealed; only a selection that came from
  elsewhere (deep link, new finding) scrolls into view.
- A render that hands focus back to the row that had it (Scanner `renderScan`, by issue
  title) must be told where a key move is going (`renderScan({focusTitle})`). Left to
  itself it reveals the starting row again and a jump past the window (End, Home, a
  wrap-around arrow) lands back where it began, with the selection moved but the list not.
- A windowed `role="option"` list needs `aria-posinset` and `aria-setsize`.
  Stamp them (and the roving `tabindex`) after rendering, outside the row markup that
  keyed reconciles compare, or every live insert re-renders every mounted row
  (History: `syncRowA11y`).
- In a keyed reconcile, resize the pads before anything can force layout: a `focus()`
  with rows removed and the old pads clamps `scrollTop` to the briefly shorter content
  and the list jumps (History's End key landed ~90 rows short).
- app.js treats focus inside `[role="listbox"]` as typing (for select typeahead). A
  list whose rows take single-key shortcuts must be excluded there (`:not(#rows)`).

## Verifying

- Pure math: `ui/_js-tests/list-rows-model.test.mjs`. Glue behaviour: the real
  `createListView` is sliced out of `core.js` and run with a fake DOM in
  `windowed-list.test.mjs`; keep a regression test per bug found there. Feature-level
  key and focus behaviour: `scanner-keys.test.mjs` slices the real `renderScan`,
  `moveScanSelection` and friends out of `scanner.js` and runs them on that same
  stack, so a focus bug that only shows when a render and a key move interact is
  caught without a browser.
- Structure: `internal/control/ui_virtual_lists_test.go`.
- In a browser, seed through REST or HAR import with `example.com` only, on
  non-default ports, and check `lsof` first: scratch directories can be shared with
  other agents' instances. Playwright's own scroll-into-view replaces a virtualized
  row under it, so act on coordinates. A click on a Findings row moves focus to the
  detail by design; focus a row before sending arrow keys. Check 390px, where Back
  returns to a list that was `display:none`.
