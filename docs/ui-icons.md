# UI icon system

Every icon in the control UI is a purpose-drawn vector symbol in one inline
sprite (`<svg id="iconSprite">` in `internal/control/ui/index.html`). Icons use
`currentColor`, so they follow the dark, light, high-contrast and forced-colors
themes with no extra work. They are not raster images, emoji or font glyphs.

The sprite is inline so the UI needs no extra request at boot. The login page is
a separate document with its own single inline lock glyph drawn to the same
rules. The mobile dock reuses the main sprite.

## Style rules

| Rule | Value |
|---|---|
| Grid | 24x24 `viewBox`, 2px safe padding (live area 2-22) |
| Stroke | 1.75 units, set once by `.icon` in `app.css`; symbols never set a stroke width |
| Caps and joins | round |
| Corner radius | 2 for rectangles and bends; 1 to 1.5 on small details |
| Fills | none. A deliberate dot is a zero-length stroke or a tiny circle (r <= 1.8) |
| Complexity | aim for 3 sub-shapes or fewer; hard limit 6 and 700 bytes of shape data |
| Colour | none in the symbol; the element's `color` decides |

**Why 1.75.** At the 16px default (`.icon` is 1.15em) a 2-unit stroke renders at
1.33px and closes the 2-unit gaps inside busier icons (certificate, phone with
signal, radar) into blobs. 1.75 renders at 1.17px, stays crisp on 1x displays and
still reads at 20px nav and 24px section headings. Empty-state illustrations
override it in CSS (`.state-empty-icon .icon`).

**Signature motif.** The Interseptor mark (`i-proxy`, also the logo and
favicon) is two opposed lanes, a request out and a response back, with the
barbed arrowheads on the outer sides. The family echoes that idea: things that
sit *in the path* are drawn as a lane meeting a gate or a node. `i-intercept` is
a lane stopped at a hold bar, `i-intruder` fans one lane out against a wall,
`i-repeater` is two lanes closed into a loop, `i-scanner` is a sweep line over
open rings, and `i-map` is a tree of nodes.

## Vocabulary by domain

- **Workspaces.** `proxy`, `intercept` (hold gate), `repeater` (loop), `intruder`
  (payload fan), `scanner` (radar), `map` (site tree), `finding` (flag),
  `notes` (notebook), `activity` (pulse), `settings` (sliders), `report`
  (document), `folder` (project).
- **Engagement context.** `scope` (corner brackets around a target), `target`
  (crosshair), `evidence` (paperclip on flow rows), `readiness` (dial), `stop`
  (blockers), `info`.
- **Status and severity.** `status-todo`, `status-done`, `check`, `alert`
  (high), `alert-circle` (medium), `sev-critical` (diamond), `sev-low`, `info`
  (informational), `block`.
- **Network and security.** `tls` (certificate with seal), `lock`, `lock-open`,
  `key` (session and auth), `identity`, `globe` (host), `oob` (callback
  antenna), `device-mobile` (phone with signal), `api-mcp` (braces around a
  node), `checks` (shield with tick), `agent` (AI actor).
- **Actions and objects.** `plus`, `close`, `trash`, `copy`, `search`, `save`,
  `edit`, `link`, `attach`, `diff`, `refresh`, `download`, `arrow-up`,
  `arrow-down`, `external`, `fit`, `parallel`, `filter`, `tag`, `note`,
  `columns`, `panel-right`, `panel-bottom`, `chevron`, `more`, `tools`,
  `decode`, `codec`, `folder-open`, `archive`, `clipboard`, `flask`, `clock`,
  `keyboard`, `rocket`, `sun`, `moon`, `appearance`, `intent`, `storage`,
  `cluster`, `timeline`, `list`, `grid`.

One meaning, one icon: ids are semantic (what the icon *means*), never aliases
of each other, and a meaning that changes gets a new id. The test suite fails on
a symbol with duplicate geometry.

## Using an icon

- Static markup: `<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-proxy"/></svg>`.
- Scripts building HTML strings: `icon('proxy')` from `js/core.js` (pass a label
  only when the icon stands alone: `icon('lock','HTTPS')`).
- Icon-only buttons need an `aria-label`; a `title` is not an accessible name.
- Size with the existing classes (`.icon`, `.icon-lg`, `.nav-icon`, dock and
  chip rules). Do not set `width` and `height` attributes on the `<svg>`.
- When the label hides on narrow screens, keep the icon in markup and hide the
  text (`.lbl-long` / `.lbl-short`, `.mob-ico`), never draw glyphs with CSS
  `content:`.

## Adding or changing an icon

1. Draw it on the 24 grid with the rules above and add one `<symbol>` to the
   sprite, in the group where its neighbours live.
2. Use it. A symbol with no call site fails `TestUIIconReferencesResolveAndNoDeadSymbols`.
3. Render the sheet and look at it at 16, 20 and 24px in both themes:

   ```bash
   node scripts/ui_icon_sheet.mjs /tmp/icon-sheet.html
   ```

   It must be distinguishable from its neighbours at 16px; if two icons look
   alike, change the less common one.
4. Run `go test ./internal/control/ -run TestUI`. The tests check the viewBox,
   currentColor-only paint, size, resolving references, accessible names,
   retired ids and that no emoji or Unicode glyph stands in for an icon.

Two text glyphs remain by design: `▸` marks run/next inside text button labels
(`Send ▸`) and `★` marks the suggested option inside a native `<option>`, which
cannot hold SVG.
