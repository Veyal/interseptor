---
name: versioned-ui-assets
description: How the embedded UI is cached (/_v/<build id>/ immutable assets, revalidated shell), and the rules that keep it correct. Use when adding a stylesheet, a module entry point, a dynamic import or any code that names a UI asset by URL, or when a UI change does not show up after a rebuild.
---

# Versioned UI assets

`serveUI` (`internal/control/control.go`) and `ui_assets.go` serve the embedded UI in two tiers.

- **The shell (`/`)** is `Cache-Control: no-cache` with an `ETag`. It is the only response the
  browser revalidates. `versionUIShell` rewrites every `src`/`href` in `index.html` that names an
  embedded `.js` or `.css` file to `/_v/<build id>/<path>` when the shell is served; the file on
  disk keeps plain `/js/…` and `/css/…` paths, which is what the source-text tests assert on.
- **`/_v/<build id>/…`** is `private, max-age=31536000, immutable`. The build id is a digest of
  every embedded file (`uiBuildID`), so any UI change produces new URLs.
- **A stale build id** serves the current file as `no-cache`. A tab opened before an upgrade keeps
  working and cannot poison the cache.
- **Unversioned paths** (`/js/app.js`) still work and are `no-cache`, as before.

## Rules

1. **One URL per file.** A module reached through two URLs is evaluated twice, with two copies of
   its state. Never write a literal `/js/x.js` or `/css/x.css` in JavaScript. Derive it:
   `new URL('../css/x.css', import.meta.url).href`, or use a relative `import('./x.js')`.
2. **Match stylesheets by suffix.** A presence check such as
   `document.querySelector('link[href$="/css/x.css"]')` survives the prefix; `href="/css/x.css"`
   does not, and the sheet is appended a second time after the mobile and desktop overrides.
3. **New stylesheet** = a `<link rel="stylesheet" href="/css/x.css">` in `index.html`. The rewrite
   picks it up; `scripts/desktop-type-overrides.mjs` reads the same links, so run
   `node scripts/desktop-type-overrides.mjs` if the sheet sets a bold weight or uppercase.
4. **Only shared tokens in web stylesheets.** `--surface-*`, `--border-subtle`, `--text-*` exist
   only in `css/desktop/tokens.css` (scoped to `html.desktop-app`). A web rule that reads them
   resolves to nothing in a browser. Use `--bg*`, `--fg*`, `--line*`; the desktop skin remaps those.

## Measuring

`scripts/ui_smoke.sh` proves boot. For load cost, drive headless Chrome with CDP
`Network.emulateNetworkConditions` and compare a cold load with a second `goto`: a warm reload
should transfer the shell and the API calls only. On loopback the difference is small; at 40 ms of
latency it is seconds, because ~190 conditional requests queue six at a time.

## What this does not fix

A cold first load still fetches about 150 modules one by one. Removing that needs either hints for
the whole static graph or a bundle step, which the project deliberately does not have.
