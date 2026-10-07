---
name: collections-ui
description: Conventions for the Collections panel UI (js/collections*.js)
---

# Collections UI

- Split by concern: `collections-model.js`, `varscope-model.js`, `scriptedit.js` are pure (node-tested, no DOM); `collections-{core,tree,editor,env,response,sheets}.js` hold DOM wiring; `collections.js` is the entry (loading, palette commands, shortcuts).
- Shared state lives in `S` from `collections-core.js`; never import `collections.js` from sibling modules (cycle).
- Server strings go in via `textContent` or `esc()` only. Secret variable values are masked client-side and must never be rendered or put in toasts.
- Sheets opened here must have ids registered in `MODAL_IDS` (core.js): `collEnvSheet`, `collScriptsSheet`, `collImportSheet`, `collRunSheet`.
- Script trust is only requested from the UI session; the UI never offers trust for AI-sourced scripts.
- `dock-model.js` pins the phone panel set in its test; the phone reaches Collections through the "All tools" select, not the bottom dock.
- Real-browser pass lessons: a `.x-table td{white-space:nowrap}` rule beats a bare `.row-name{white-space:normal}`, so scope the wrapping rule as `.x-table td.row-name`; sheets are about 420px wide on desktop, so tables that hold inputs need a container query (`.coll-vars-wrap`) that turns rows into labelled cards instead of squeezing columns; the shell's `.sheet-body` has 16px bottom padding, so a sticky footer needs `bottom:calc(-1 * var(--sp-4))` or scrolled content shows under it; never leave a native `<input type=file>` visible (hidden input plus an app button). `ui_collections_layout_test.go` pins these.
