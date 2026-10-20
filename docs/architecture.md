# Architecture

## Security model

The control plane has **two trust modes** (`internal/control/guard.go`):

- **Loopback trust (default, unchanged).** Both listeners bind **loopback** by default. A request
  that arrives on a loopback connection with a loopback `Host` and no API key is allowed — this is
  how the embedded UI, curl, and the in-process MCP tool bus reach it. The control plane additionally
  **rejects any request with a non-loopback `Host` header or a non-loopback `Origin`**, so a web page
  you happen to visit can't quietly drive the API (CSRF) or read your captured traffic via
  DNS-rebinding. Rebinding the **proxy** or **control UI** to a non-loopback address (e.g. `0.0.0.0`
  for LAN device capture) is allowed from Settings; set `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0` to refuse
  non-loopback binds.
- **Proxy listener exposure.** Proxy listeners accept traffic with no credentials by default.
  Optional basic auth is a username and password configured in Settings; it is not an API key.
  Binding to a non-loopback interface is still an explicit operator decision (guarded by
  `INTERSEPTOR_ALLOW_EXTERNAL_BIND=0`). With authentication off, that listener is an open proxy for
  anyone who can reach it. `Proxy-Authorization` is stripped before forwarding. History and the
  control API still require API keys for remote (non-loopback) access. See
  [Proxy listener exposure](proxy-and-tls.md#proxy-listener-exposure).
- **Key-authorized remote access (opt-in, added in v0.29.0).** A request carrying a valid API key is
  authorized regardless of Host/Origin/connection — this is what lets an AI agent on a VPS or a
  collaborator's browser reach Interseptor over a tunnel. Keys are **scoped**: a **read**-only key may
  only view (GET/HEAD + the SSE stream); a **full** key may also mutate. Browser access goes through
  `/login`, which mints an httpOnly session cookie; cookie-authenticated mutations additionally
  require an anti-CSRF header and a same-origin `Origin`, since a cookie is an ambient credential (the
  bearer-token path has no such requirement, since a bearer token isn't ambient). The `/mcp` endpoint
  always requires a **full**-scope key when any key exists. A non-loopback request with no valid key
  is closed outright (401, or redirected to `/login` for a browser navigation) — so accidentally
  exposing the port never leaks captured pentest data. The optional **Cloudflare quick tunnel**
  (Settings → API & MCP → Share) is opt-in and **refuses to start unless at least one API key already
  exists**, so the tunnel can never expose an unauthenticated instance.

Captured traffic never leaves your machine unless you deliberately expose it through remote access or an external MCP/REST client. Interseptor has no built-in model provider and stores no provider keys.


**Data at rest is unencrypted.** Captured requests/responses — which can include credentials, session
tokens, and other PII from whatever you're testing — are stored **unencrypted** under `~/.interseptor/`
(`interseptor.db` SQLite database + content-addressed body files). Interseptor does not encrypt this data at rest;
securing the machine and disk it runs on is the operator's responsibility.

For the vulnerability-reporting policy (bugs *in* Interseptor itself), see [SECURITY.md](../SECURITY.md).

## Package layout

One Go binary, two localhost listeners. Each `internal/*` package has a single responsibility and is
independently tested.

| Package | Responsibility |
|---|---|
| `internal/store` | SQLite metadata (flows, rules, settings, issues, ws frames, scope, views, keys) + content-addressed body files |
| `internal/capture` | Stream bodies to the store via `io.TeeReader` (never buffered whole) |
| `internal/tlsca` | Local CA: load/generate, mint + cache per-host leaf certificates |
| `internal/intercept` | Hold queue (forward/edit/drop) for requests **and** responses + match-&-replace |
| `internal/proxy` | Forward proxy, `CONNECT` + TLS MITM, WebSocket frame relay, flow capture, upstream proxy |
| `internal/scope` | Target-scope include/exclude matcher (host wildcards + path prefixes) |
| `internal/preview` | Pure-Go PNG renderers: flow viewer plus the evidence render family (Intruder timeline/distribution/race/strip, authz matrix, flow diff, flow waterfall, finding chain); input structs are plain data, no imports of intruder/control/store |
| `internal/sender` | One-off direct request sender (+ session headers, CSRF/re-auth token macro, authz replays) — backs Repeater & Intruder |
| `internal/intruder` | Sniper / Pitchfork / Race attack engine (threads, delay, grep-match/extract, payload processing) |
| `internal/scanner` | Passive security checks over captured flows |
| `internal/oob` | Out-of-band interaction catcher (blind SSRF/XXE/SQLi/RCE callbacks) |
| `internal/checkscript` | Runs user-authored Starlark scanner checks (sandboxed, bounded) |
| `internal/msgcodec` | Project-scoped Starlark message codecs (app-layer encrypt/decrypt for History/Repeater; never on the proxy hot path) |
| `internal/curlgen` · `internal/report` | Render a flow as `curl`; render findings as Markdown |
| `internal/wsrepeater` | WebSocket Repeater (RFC 6455 handshake + masked frames, no deps) |
| `internal/harx` | HAR 1.2 import/export |
| `internal/sysproxy` | Opt-in macOS system-proxy toggle |
| `internal/verify` | Deterministic verification primitives for replay and evidence workflows |
| `internal/android` | Configures a USB-connected Android device for HTTPS interception via `adb` (CA install + proxy config) |
| `internal/ios` | Configures iOS simulators (via `simctl`) and physical devices (`.mobileconfig` profile, or SSH automation for jailbroken devices) for HTTPS interception |
| `internal/tunnel` | Manages a Cloudflare quick tunnel (`cloudflared` child process) exposing the control plane at a public `https://*.trycloudflare.com` URL |
| `internal/launcher` | Disk-backed registry (`~/.interseptor/instances.json`) of running per-project instances + port allocation, backing the `interseptor launcher` dashboard (`cmd/interseptor/launcher.go`) |
| `internal/codec` | Pure encode/decode transforms (base64, URL, hex, HTML entities, JWT inspection, smart auto-decode) behind the Decoder tool and MCP `decode` |
| `internal/auth/jwtextract` | Pulls JWT-shaped tokens out of flows (header/JSON/query/cookie) for cross-host token replay and SSO authz testing |
| `internal/mcp` | MCP server (stdio + Streamable-HTTP) over the control API |
| `internal/control` | REST + SSE API, security guard, serves the embedded web UI |
| `cmd/interseptor` | Config, wiring, lifecycle (both listeners, runtime rebind, graceful shutdown) |

## Capture write path

Capture is best-effort and off the forwarding path. Three parts of the store exist to keep it that
way under burst traffic.

**Flow metadata is written behind a queue.** The proxy enqueues a flow's insert at request time and
its update at response time and never waits on SQLite. One flusher goroutine drains that buffer in
FIFO order, so a flow's insert always lands before its update. The buffer holds 4,096 queued writes;
past that the oldest is dropped and counted, which is reported as dropped flows rather than slowing
the proxy down.

The flusher applies the buffer in chunks of 512 writes, one SQLite transaction per chunk, instead of
one transaction per write. Its failure policy is **commit the chunk whole, or replay it write by
write**: a single queued write is several statements (the row, its full-text index entry, its tags),
so the first error abandons the whole transaction — which has committed nothing — and the chunk is
replayed one write per transaction. Every good write still lands, the bad one fails alone, nothing is
left half-applied, and the flusher keeps going. The alternative, a savepoint around every write, cost
roughly twice as much on every chunk whether or not anything failed, because SQLite keeps a
sub-journal while a savepoint is open.

A queued flow keeps ownership of its captured bodies until its chunk has committed. Body garbage
collection can therefore never see a body as unreferenced while the row that points at it is still
uncommitted. That ownership is released for the whole chunk under one hold of the body lock, not once
per write: the proxy takes that same lock for every finalized body, and once the commits were batched
it was lock contention with capture — not SQLite — that decided how fast the flusher could drain.

**Body garbage collection does not hold the capture lock.** `GCBodies` walks the content-addressed
body directory, which takes seconds once an engagement has tens of thousands of blobs. It takes the
body lock only to copy the set of bodies writers currently own, and then again for each individual
unlink — never across the walk or the database query, both of which the proxy's `Finalize` would
otherwise queue behind. Three things make the shorter hold safe:

- The owned-body snapshot is taken **before** the database is queried. A body is released only after
  the flow row referencing it has committed, so a hash the snapshot misses had its row committed
  before the snapshot, and therefore before the query.
- Every hash a writer claims while the pass runs is recorded, so a body published mid-walk survives —
  including the awkward case of a capture deduplicating onto a file the walk has already listed.
- The decision to delete and the unlink happen under one lock hold, so a `Finalize` racing that
  unlink either already owns the hash, and the file is spared, or finds the file gone and writes it
  again from its temp file.

A body finalized outside that ownership handshake — a HAR import, a finding screenshot — is claimed
by a pass already running, but takes no lasting ownership, so a pass that *starts* between its write
and the row that references it still sees an orphan. That is why those callers write their
referencing row promptly.

**Page cache.** Every pooled connection is opened with a 32 MiB page cache (four connections, so at
most 128 MiB) because the default 2 MiB evicted pages inside a single scan. `mmap_size` and
`temp_store`, which were also proposed, are deliberately not set: neither beat the cache bump on any
measured query, memory-mapping made the History sorts slightly slower and distinctly worse alongside
a large page cache, and a memory temp store had nothing left to move once the flusher stopped opening
a savepoint per write. See [Benchmarks](benchmarks.md#sqlite-pragmas).

## Leaf certificate keys

`internal/tlsca` mints a per-host leaf certificate on the first handshake to each host and caches it.
Minting runs with no lock held, so handshakes to different hosts mint in parallel; requests for the
same host collapse onto one mint, so a page opening ten parallel connections to a new host costs one
keygen and one signature rather than ten.

**Every leaf gets its own private key.** Sharing one leaf key across every minted certificate is
common proxy practice and was the obvious thing to try here, and it is deliberately not done:

- It is not where the cost was. An ECDSA P-256 keygen is about 8µs of an 84µs mint; the signature
  and parse are the rest. What made concurrent handshakes slow was minting under an exclusive mutex,
  and releasing that lock moved a parallel sweep of fresh hosts from 84µs to 18µs per certificate.
  A shared key would shave about a tenth off a cost that is no longer on anyone's critical path.
- What it would give up is small but real, and free to keep. With per-host keys, a key recovered from
  one intercepted connection authorises impersonating that one host. With a shared key it authorises
  impersonating every host this proxy has ever served. Neither is worse than losing `ca.key`, which
  sits on disk in the same directory and can mint anything — but a tool whose job is holding other
  people's TLS should not widen that blast radius to save 8µs.
- The practice it would copy exists to avoid a keygen per connection in runtimes where a keygen is
  expensive. Here it is cheaper than the signature that has to happen per host anyway.

Certificates stay distinct per host regardless — their own serial, subject and SAN — which is what
the cache keys on and what a device certificate viewer shows.

## Web UI

The web UI lives in `internal/control/ui/` (embedded via `//go:embed`): an `index.html` shell,
`app.css`, and native ES modules under `js/`. `core.js` owns shared UI primitives, `motion.js` owns
state-driven motion, and `project.js` owns active-project readiness plus the one lazy Map loader
shared by main navigation and Proxy's **Search in Map** action. Feature behavior stays in its
feature module; `app.js` owns global navigation, shortcuts, SSE dispatch, and boot. No build step or
bundler; the binary stays single and static.

Modules coordinate without importing each other through three layers, lightest first: the
synchronous `bus.js` publish/subscribe bus (a module emits an intent — e.g. "reload History flows" —
and whichever module owns that behaviour listens), the `core.js` `registerHook`/`getHook` mediator
(one implementor answers a question), and the `shell-hooks.js` SSE dispatcher. The bus closed the
`proxy → findings → settings` and `proxy → tags/authz → proxy` ESM import cycles so a change in
evaluation order can no longer raise a Temporal Dead Zone `ReferenceError` at boot;
`ui_import_cycles_test.go` fails on any new static import cycle. `index.html` `modulepreload`s the
root modules (`core`, `bus`, `proxy`, `intercept`, `settings`, `findings`) so the first import wave
loads in parallel instead of one depth at a time.

The control server serves every embedded UI file with `Cache-Control: no-cache`, a strong per-file
`ETag` (a reload is a set of body-less 304s, yet an upgrade is picked up at once) and a gzip copy
built once per file when the browser accepts it (`ui_assets.go`). `core.js` `api()` lets concurrent
identical option-less GETs share one request (`api-inflight.js`); a mutation or a live event clears
the shared reads, so a read issued after a change never joins an older one. `app.js` keeps
`document.title` on the focused panel (`<Panel> · <project> · Interseptor`, `shell-title.js`) and
gives each panel a visually hidden `h2`.

Findings uses `finding-workspace.js` for section routes and filtering, with
`findings.js` retaining data and editor ownership. `findings.css` styles its
searchable list, section reader and inline evidence. `surfaces.css` supplies
shared popup and disclosure styling; `surface-position.js` bounds floating
menus to the visual viewport. These assets are embedded alongside `app.css`.
The Projects dialog is `project-picker.js` (view, `css/projects.css`) over the pure
`project-picker-model.js` (grouping, Recent, filtering, keyboard index math, new-project
validation, path keys); `settings.js` keeps the single `doSwitchProject` path and injects it with
`configureProjectPicker`, so the picker never imports `settings.js`. Phones reach it from the top-bar
`#mobileProjectChip`, wider windows from the rail-foot `#projBadge`.
`listbox.js` gives JS-rendered single-select lists their listbox/option
semantics and one roving Tab stop. `index.html` carries no inline colour,
spacing, typography, or width styles (`TestUIIndexHasNoInlineDesignStyles`);
layout comes from the `app.css` tokens and its small `.u-*` utility layer, and
the few remaining inline `display:none` values exist only because a script
resets them with an empty value.

### Evidence workbench shell

The chrome answers "am I in scope, what evidence do I hold, what blocks my report?" before any
panel does, in one bar: the **Flight Bar** (`#bar`, a `<header>` 42px tall at the default density).
Left to right it holds the brand, the project and scope pills, the centred Ctrl/Cmd+K omnibar
(`#cmdkBtn`), the intercept status, the active identity, the readiness ring, the live connection chip
and the theme toggle. It replaced a 48px topbar plus a 49px Engagement Strip (97px of chrome), so
the History list gains 55px (544px to 599px at 1440x900, 364px to 419px at 1280x720). `ctxbar.js`
renders it from `project-state.js`, a small store with `get`, `subscribe` and a debounced
`refresh`. The store
reads `GET /api/project/readiness` (falling back to per-endpoint reads), marks a failed segment
stale on its own, and limits `flow.new` to one refresh per two seconds. The client renders the
server's readiness verdicts and never recomputes pass or fail.

The readiness ring (`#ctxRing`, a real button with `aria-haspopup` and `aria-expanded`) opens a
non-modal popover that holds what the strip used to show permanently: the Next action, the
target, the evidence counts and the report blocker checklist (project blockers first, then one row
per blocked finding with a Fix link). The popover closes on Escape or Close (focus returns to the
ring), on an outside click and when focus tabs out of it; a row that leaves for another surface
parks focus on the ring, which the surface it opens then takes. Readiness stays report-time information: the ring shows the ready fraction, its
arc and the blocker count only while the Report view is open (`shell-hooks.js` publishes that
signal), and otherwise flags only missing project setup, with a "!" shape rather than colour alone.
The pure rules (ring state and accessible name, popover meter, checklist) are in `ctxbar-model.js`
and tested under node. The bar adds no motion. Narrow windows shorten it in tiers instead of
clipping it: 1100px drops the brand text, 900px shortens the pills and the intercept status
(`#icptStat` stays for assistive technology, a short `#icptCompact` twin shows `REQ · 2 held`),
drops the shortcut hint and the identity avatar and turns a connected live chip into a dot, 720px
hands the project pill and the identity to the dock's More sheet and the palette (`@`), 480px does
the same for the theme toggle, and 360px drops the logo mark; the omnibar, scope pill, ring and
live chip are never removed, and a window narrower than that scrolls the bar sideways. `connection.js` holds the Connection popover (listener addresses, device proxy,
live-update state) and the five-second offline banner. `cmdk.js` (with `cmdk-logic.js` and
`cmdk-actions.js`) is the command palette: fuzzy ranking from `keys.js`, `>`/`f:`/`#`/`@`/`?` prefix
modes, and an action for every legacy dialog id so replaced dialogs stay reachable. `keyboard.js`
owns the shared key registry, chord continuations and the "Single-key shortcuts" switch.

`flowdrawer.js` is the one flow inspector — a docked `complementary` region above 1100px, an overlay
with core's focus trap below — fed by the pure `flowbody.js`. Every panel opens a flow through core's
`openFlow`/`closeFlow` hooks; the old centered `flowmodal.js` is deleted and the drawer carries its one
distinct feature, a Current/Original **Compare** control shown only for a flow modified in Intercept.
The Proxy in-panel inspector (`#inspect` with `#inspectSplitter`, `proxy.js`) is always the bottom inspector for History selection (there is no side-drawer docking mode there; double-click opens the drawer) and stays as the fast-path view set and gained the
structured sub-tabs from the pure `inspector-structure.js`: a Headers grid with cookie parsing, a
collapsible JSON tree with JSONPath copy, a Query/Form parameter table, a GraphQL viewer, and an
inline image preview, plus a "Pause live stream" freeze that holds the history viewport while capture
continues. `evidence-attach.js` and `evidence-tray.js`
attach, order and undo evidence through the existing finding endpoints, and `readiness-meter.js`
draws a finding's server-reported stage as `role=meter`. `report-preflight.js` is the Findings
Report sub-view with gated export. `checklist.js` is the first-run checklist, derived from real
state.

The central workspace is a **multi-document canvas** (`canvas.js`, pure state in `canvas-model.js`,
styles in `canvas.css`) rather than a singleton `.panel.active`. The rail is the only navigation surface — there is no open-document tab strip — and one or two panes (`#canvas`) fill the whole workspace. Split and close live in an action cluster in the Flight Bar (`#paneActions`, in normal flow so it never covers a panel toolbar, hidden at 720px and below) that acts on the focused pane: `#splitLrBtn`/`#splitTbBtn` keep their `aria-pressed` state and `#paneCloseBtn` closes the focused pane's document (shown once more than one document is open; the next open document is revealed, and any tool reopens from the rail). Switching documents is the rail's own Up/Down roving focus; the former tab-strip arrow-key and `Delete`/`Backspace` handling is gone. Opening a tool — from
the rail's `activateTab`, the command palette, a deep link or a Send-to flow — opens or focuses that
tool's document; the canvas moves the singleton tool panels between the visible pane(s) and a hidden
`#canvasPool`, so the panels keep their global state and the existing `openFlow`/`closeFlow` and
list-rendering code is unchanged. **Split left/right and top/bottom** (`#splitLrBtn`/`#splitTbBtn`,
a keyboard-operable `role="separator"` sash reusing `layout-math.js`) show two documents at once;
the split is desktop-only and collapses to a single pane at 720px, where the dock switches the
focused document. The decisive invariant is that `.panel.active` now means *mounted in a visible
pane* — one in a single layout, up to two when split — so **panel-gated SSE stays correct with two
panels visible**: a Proxy panel sharing the screen keeps rendering `flow.new` (its store is updated
before the gate, so only the live DOM patch is gated), while a panel in no visible pane still pauses
and badges. The shell gates live and resync work on panel visibility (`isPanelVisible`), keyboard
shortcuts and the entrance motion target the focused panel (`.is-focused`), and `emitTabChange` fires
on every focus change so `collections.js`, `settings-health.js`, `report-preflight.js` and `dock.js`
stay in step. The layout (open documents, split orientation, ratio) persists per project. Each
document maps 1:1 to a tool panel for now; per-instance documents (two Repeater tabs) and the
multi-monitor `BroadcastChannel` popout inspector are deferred. Shared primitives live beside them: `split.js` (SplitPane), `sheet.js` (BottomSheet),
`statepanel.js` (empty, loading, error, offline and locked states), `diff.js`, `copyas.js`,
`finder.js`, the `bus.js` event bus, and `core.js`'s `createListView` — the one windowed-list
renderer with container-level event delegation (built on the pure `virtual-list-model.js` windowing
math) that now backs Intruder results and the WebSocket frame list; `windowed-list.js` (with its `list-rows-model.js`) layers group headers and a sticky header on top of it for the Findings and Scanner-issue lists, so a project with thousands of findings or issues renders a bounded window of rows. The Decoder is a slide-up bottom
"Convert" drawer reachable anywhere with `Cmd/Ctrl+J`. `copy-image.js` is the DOM-free logic behind the Findings **Copy image** button (PNG
conversion, a promise-valued `ClipboardItem`, secure-context fallbacks). `dock.js` is the phone bottom navigation; it calls the shell's `activateTab`, and the
rail (`#tabs`) is `display:none` at 720px and below so assistive technology sees one navigation.

Deliberate differences from the overhaul spec, recorded so they are not mistaken for gaps:

- **Repeater send-as.** The replay API has no identity parameter; an identity is applied by editing
  the request headers. The Proxy context menu's "Send as" therefore opens a Repeater tab whose title
  carries `[identity]`, and no per-tab override control is shown because it could not be honest about
  what the server will do.
- **Hint strip.** The 24px panel-footer binding list is deferred: every binding is already listed in the
  shortcut sheet and the command palette, and the footer would cost vertical space on phones.
- **Notes layout.** Notes keeps its single-column list and editor; Map and the Proxy inspector own
  their own dock layouts. Only Findings, Intercept, Scanner and Settings use `split.js`.
- **Intercept auto-forward count.** The server keeps no counter, so Intercept tallies out-of-scope
  requests from the live flow stream with the exact client scope mirror and hides the line when scope
  cannot be decided in the browser (regex rules).

Stylesheets load in a fixed order: `app.css` (tokens and legacy components), `surfaces.css`,
`findings.css`, `primitives.css`, `shell.css`, `flow.css`, `panel-proxy.css`, `panel-tools.css`,
`panel-scan.css`, `panel-misc.css`, `settings.css`, `report.css`, `canvas.css` (the document-tab
strip, panes and split sash), `desktop.css` (rules scoped to `html.desktop-app`), and `mobile.css` so the phone model wins the web cascade. The desktop skin loads last: `css/desktop/tokens.css`, `type.css`, `states.css`, `components.css` and `shell.css`, every rule scoped to `html.desktop-app`.
`workbench.css` documents the 1100/900/720/480px breakpoints; it holds no rules, so it is embedded
but not linked.

Desktop shells run the same UI. `desktop.js` (pure rules in `desktop-model.js`) adds
`desktop-app` plus `desktop-<platform>` to the root when the native macOS shell has injected
`window.interseptorDesktop` or when the page runs in a Chrome/Edge `--app` window, which matches
`(display-mode: standalone)` while a normal tab does not. Only then does it block browser reload,
history and page-zoom gestures and apply `desktop.css`. For the native shell it attaches
`interseptorDesktop.run(command)` and posts `ready`, `theme` and deduplicated `status` messages;
`app.js` injects every action (panel, intercept toggles, palette, project picker, theme) so
`desktop.js` imports nothing but its model. In every mode it keeps `<meta name="theme-color">` on the
Flight Bar colour, and the server serves `manifest.webmanifest` with an SVG icon.

Inside a desktop host the UI also wears the desktop skin (`docs/desktop-design.md`). `css/desktop/`
remaps the existing tokens (`--bg*`, `--fg*`, `--line*`, `--accent*`, `--fs-*`, radii, density
sizes) under `html.desktop-app`, `html.desktop-app[data-theme="light"]` and
`html.desktop-app[data-density]`, so every panel moves at once; high contrast keeps its own colours
and focus ring. `type.css` is generated by `scripts/desktop-type-overrides.mjs` (weights 400/500/600
only, no uppercase transforms). `states.css` replaces the yellow ring with a keyboard-only 2px accent
ring: `desktop.js` sets `html.dsk-kbd` on a navigation key and clears it on a pointer press or a
native command, so focus moved by code after a menu command never paints a ring. `desktop-shell.js`
(called by `app.js` only when `isDesktopApp()`) adds the intercept pill to the 38px title toolbar,
builds the 24px status bar from `statusBarModel` in `desktop-model.js` (project at bottom-left, followed
by proxy, intercept, scope, flows, warnings, connection, version) and registers palette entries
for the controls the toolbar hides. The web markup is never changed.

Theme and density are root attributes set by a pre-paint script so there is no layout shift:
`data-theme` is `dark`, `light` or `hc` (high contrast; `forced-colors` maps borders, focus and
selection to system colours) and `data-density` is `compact`, `default` or `comfortable`
(`pointer:coarse` forces 44px rows and targets). Layout tokens include `--topbar-h` (the Flight Bar:
42px, 36px compact, 50px comfortable, 54px on touch), `--bottomnav-h` and `--sticky-top`, which feeds
`scroll-padding-top` so focus is never hidden under the sticky bar. Only `transform` and `opacity`
animate, and the global reduced-motion block turns every animation and transition off.
Single-letter shortcuts (`e`, `d`, `x`, `y` then a letter, `/`)
fire only outside editable controls and open dialogs and can be switched off in Settings; each has
a visible button. Pure logic (diff, fuzzy scorer, chord resolver, copy-as, blocker derivation,
selection model) is tested with `node --test` under `internal/control/ui/_js-tests` (not
embedded), and `ui_a11y_audit_test.go` sweeps every stylesheet, script and the shell markup for
accessible names, focus rings, target sizes, motion, glyph icons and privacy of the locked page.

Workspace startup has two independent safety boundaries. A small classic-script guard runs before
the ES-module graph and replaces the static loading state with a Reload action if those modules
cannot load or evaluate. Once `app.js` is running, project identity and Repeater/Intruder state
hydration race their requests against real settling deadlines; `AbortController` is used for
cleanup, not as the promise-settlement guarantee. A failed saved-state read unlocks the workstation
with its browser-local drafts intact, while an unresolved project identity keeps project-scoped
tools locked rather than selecting a guessed project. The pre-module lock is reversible if a slow
module eventually completes. A terminal startup alert clears navigation and panel `aria-busy`
without enabling unavailable controls, so assistive technology receives a settled failure rather
than an indefinite loading state. Browser-local tab envelopes are schema-checked and bounded before
normalization or rendering; unsafe values remain available for recovery and cannot be replaced by
the automatic blank-tab write. All Repeater creation routes share the same reload-safe tab and ID
limits. Browser storage keys use the versioned, percent-encoded canonical project directory:
uniquely owned localStorage and IndexedDB history keys migrate once, while an ambiguous older key is
left intact and reported instead of being assigned to the wrong project. If only the canonical
directory is available while project-list ownership is unknown, migration or removal of an affected
unscoped or name-keyed legacy value is deferred. An unscoped value also stays untouched when several
projects make its owner ambiguous, including duplicate display names; malformed or blank project-list
entries never count as ownership proof. New edits still use the collision-free canonical-directory browser
key and continue synchronizing with the project database. Client-side persistence
uses the project API's 4 MiB UTF-8 byte limit and retains larger browser drafts without retrying a
request the server cannot accept. A browser-local write failure is visible but does not suppress the
project-database synchronization path. Replacing an ignored malformed pending marker with a valid
explicit edit immediately re-enables that synchronization path.

The [UI motion specification](ui-motion-spec.md) owns motion behavior and constraints, and the
[UI icon system](ui-icons.md) owns the icon sprite, its style rules and how to add an icon. Other design
notes and per-slice specs/plans live under [`docs/`](.).

Workspace controls use app-rendered dropdowns, checkbox/radio appearances,
disclosure indicators, confirmation dialogs and contextual hints. Hidden select
elements remain only as value/change adapters for the existing feature modules;
they cannot paint a native menu, including before module startup. `hints.js`
converts static and dynamically assigned title hints into one themed surface.
Settings use a flat reading order and expandable reference text; state, errors and
material consequences remain visible. File access continues through explicit
import/export actions.
