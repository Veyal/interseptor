---
name: desktop-shell-bridge
description: Change or verify how the embedded UI behaves inside a desktop shell (native macOS WKWebView app or Chrome/Edge --app window) and the window.interseptorDesktop bridge contract.
---

# Desktop shell bridge

`js/desktop.js` (pure rules in `js/desktop-model.js`) is the only module that knows about desktop
hosts. `app.js` injects every action and status source through `initDesktop`; keep `desktop.js` a
leaf that imports only its model (`TestUIDesktopBridgeIsWiredFromAppBoot`).

- Detection: the native shell injects `window.interseptorDesktop = {platform, version, post}` at
  document start. A Chrome/Edge `--app` window matches `matchMedia('(display-mode: standalone)')`
  even without a manifest; a normal tab reports `browser`. No URL marker is needed.
- Every `desktop.css` selector starts with `html.desktop-app`; a browser tab must not change.
  Controls get `cursor:default!important`, so busy (`aria-busy`, `.is-pending`) controls are
  excluded and real links (`a[href]` that is not `.btn`) keep the pointer.
- Never add an `outline:none` focus rule: no stylesheet styles `:focus`, only `:focus-visible`, so a
  mouse click already shows no ring (`TestUIA11yFocusIsNeverRemovedWithoutReplacement`).
- core.js already replaces the browser context menu app-wide; do not add a second handler.
- Guards run in the window bubble phase so feature shortcuts (Cmd/Ctrl+R sends a flow to
  Repeater) still fire; only the browser default is cancelled. Map and the image lightbox handle
  Ctrl+wheel on their own element first, so page-zoom blocking leaves their zoom working.
- Unified title bar (native macOS): the window uses `fullSizeContentView`, a hidden title (still
  kept current for Mission Control and the Window menu) and an empty `.unifiedCompact` toolbar so
  the traffic lights centre in the 38px Flight Bar. The host object carries `titlebar:'unified'`;
  the shell sets `--titlebar-inset-left` on `<html>` (document-start script, re-applied on full
  screen, where it is 0 and the toolbar is hidden). `.fb-start` pads by it; `#bar` is a 3-column grid
  so the command field stays centred on the window. Chrome `--app` keeps its own title bar (no var).
- Window dragging: WKWebView ignores `-webkit-app-region`. desktop.js marks `#bar`
  `[data-window-drag]` and posts `{type:'drag-regions', rects, exclude}` (CSS px; `exclude` = visible
  controls matching `DRAG_EXCLUDE`) on every layout change, deduped. `WindowDragWebView.mouseDown`
  calls `performDrag` inside a rect and outside every control; double-click follows
  `AppleActionOnDoubleClick`. A new control in the bar is excluded automatically if it is a
  button/link/input or has a role/tabindex; a plain `<div>` click target would drag instead.
- File pickers: `WKOpenPanelParameters` has no accept list, so desktop.js posts
  `{type:'file-accept', accept:[tokens]}` from a capture click listener and a wrapped
  `HTMLInputElement.prototype.click/showPicker` (covers detached inputs). The shell applies it to the
  next open panel within 3s; any token without a UTType (or no accept) means all files.
- Updates: `{type:'check-updates'}` (main frame only) runs the native shell's user-initiated
  update check, the same as Interseptor → Check for Updates…; the shell always reports a result.
- `run(command)` returns a real boolean: false for unknown commands and for actions that cannot run
  yet (workspace not hydrated, intercept state unknown, a modal blocking the palette).

## Verifying

Build to a scratch path, run on non-default ports with a throwaway `--data-dir`, open headful Chrome
`--app=<url> --user-data-dir=<tmp> --remote-debugging-port=<p>` and drive it over CDP. To simulate
the macOS host use `Page.addScriptToEvaluateOnNewDocument` with a stub
`window.webkit.messageHandlers.interseptor.postMessage` that records messages, then reload. In
Chrome that hook can run before `<html>` exists (WKWebView's document start has it), so guard any
`document.documentElement` access in the stub. A first-run project shows the setup wizard, which
correctly makes `palette:open` return false.

## Desktop skin (css/desktop/, js/desktop-shell.js)

- Token blocks: `html.desktop-app` (0,1,1) loses to the web `:root[data-theme="light"]` and
  `:root[data-density=…]` (0,2,0) blocks, so light, hc and density each need their own
  `html.desktop-app[data-…]` block. Desktop component CSS reads the new `--surface-*`/`--text-*`
  names, so high contrast must redefine those too.
- The web focus ring rule is (1,1,0) because of `:not(#inspectSplitter)`; desktop focus rules borrow
  id weight with `:not(#dsk-x)` instead of `!important`. The ring paints only under `html.dsk-kbd`.
- App code paints methods/status/labels with inline `style="color:var(--…)"`; override with an
  attribute match (`[style*="--accent"]`) plus `!important`, never by editing the web markup.
- After a web stylesheet adds a 650/700/800 weight or an uppercase transform, run
  `node scripts/desktop-type-overrides.mjs` (a Go test runs it with `--check`).
- Verify both looks: emulate the macOS host with `Page.addScriptToEvaluateOnNewDocument` (stub
  `window.interseptorDesktop`) in one CDP session, and prove the web tab unchanged by pixel-diffing
  a normal tab against a build from before the change.
- One helper module: `js/desktop-panel-kit.js` owns `isDesktop`/`isMac`/`iconNode`/`el`, the
  `whenReady`/`whenDesktop` gates, `move` (re-parent and remember the web position; restored when
  `html.desktop-app` goes away) and every popup's dismissal (`registerPopup`: outside press,
  Escape, resize). Never copy these into a panel module (`TestUIDesktopHelpersLiveOnceInTheKit`).
- Overflow menus are `createOverflow` (an ellipsis button named "More actions"). Items are real
  buttons (re-parented) or descriptors `{label, icon, hint, run, disabled(), hidden(), checked()}`
  re-read on open; give every item in one menu an icon. A closed menu is `opacity:0`, never
  `visibility:hidden`: focus the app returns to a moved control must land somewhere, and the kit
  hands it to the trigger.
- Width: panels must work from the 900px minimum window up. Prefer container queries on the
  pane (`container:<name>/inline-size`) to drop labels to icons or collapse tokens to a count
  chip, over clipping or wrapping a 40px bar. `_js-tests/desktop-panel-kit.test.mjs` shows the
  fake-DOM style for behaviour tests without a browser.
