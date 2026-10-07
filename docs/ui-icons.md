# UI icon system

Every icon in the control UI is a purpose-drawn vector symbol in one inline
sprite (`<svg id="iconSprite">` in `internal/control/ui/index.html`). Icons use
`currentColor`, so they follow the dark, light, high-contrast and forced-colors
themes with no extra work. They are not raster images, emoji or font glyphs.

The sprite is inline so the UI needs no extra request at boot. The login page is
a separate document with its own inline lock glyph and two-lane mark drawn to the same
rules. The mobile dock reuses the main sprite.

## The Interseptor family: "Gate & Lane"

The family is built the way the product works: traffic runs down straight
**lanes**, and the interesting things happen at a **gate**. The logo (`i-proxy`)
is two opposed lanes (request out, response back) with a square tap pad between
them; that same mark is the favicon, the header logo and the login mark.

### Construction spec

| Rule | Value |
|---|---|
| Grid | 24x24 `viewBox`; every vertex inside the 2..22 safe box |
| Geometry | `<path>` only, commands `M L H V Z` only. Every segment is horizontal, vertical or exactly 45 degrees. No arcs, curves, `circle`, `ellipse` or `rect` |
| Corners | Chamfered at 45 degrees, never rounded: boxes use a 2-unit chamfer (1 on small details). "Round" things (status, info, block, clock face, lens, scanner, globe) are octagons |
| Stroke | 1.75, set once by `.icon` in `app.css` together with `stroke-linecap:square` and `stroke-linejoin:miter`; symbols never set stroke attributes |
| Pads | A dot is a zero-length stroke (`h.01`), which the square cap renders as a 1.75 square pad. `more`, `cluster` and `timeline` use a 2x2 closed "cell" (about 3.75 solid) where a heavier dot is needed |
| Signature | (1) lane + hold bar: icons that sit in the request path carry a lone vertical gate stroke (`intercept`, `intruder`); (2) the tap pad marks a point where traffic is observed (`proxy`, `scope`, `target`, `api-mcp`, `oob`); (3) severity is shape coded: diamond = critical, up pentagon = high, octagon = medium, down pentagon = low. The signature must NOT be added to generic UI icons (`plus`, `close`, `check`, `chevron`, arrows, `trash`, `copy`) - noise at 16px |
| Complexity | aim for 3 sub-shapes or fewer; hard limit 6 paths and 700 bytes |
| Fills | none (stroke only). Colour is never in the symbol; the element's `color` decides |
| 16px rule | no gap smaller than 2 units between parallel strokes, no feature smaller than the 1.75 pad; if two icons look alike at 16px the less common one changes |
| States | none. Active/disabled are colour and opacity from CSS; icons never swap geometry |

`ui_icons_test.go` enforces the mechanical rules (path commands, 45 degree
segments, safe box, path-only, no colour, size, signature motifs, square/mitre
stroke style) so a new icon cannot drift.

**Why 1.75.** At the 16px default (`.icon` is 1.15em) a 2-unit stroke renders at
1.33px and closes the gaps in busier icons into blobs. 1.75 renders at 1.17px,
stays crisp on 1x displays and still reads at 20px nav and 24px headings.
Empty-state illustrations override it in CSS (`.state-empty-icon .icon`).

### Directions that were drawn and rejected

All three were drawn as complete probe sets (27 icons, every category, 16/20/24/48px,
dark and light, plus a nav-rail/chip mock) before the winner was picked.

- **A. Gate & Lane (chosen).** Angular, 0/45/90 only, octagons, square pads. It
  scaled to all 81 meanings (including `sev-low`, `status-*`, `codec`, `chevron`,
  `more`) without exceptions beyond a 2-3 icon touch-up, stayed legible at 16px
  and is visibly unlike Lucide/Feather.
- **B. Packet** (orthogonal cells, one solid cell, butt caps). Rejected: with no
  diagonals and no curves, `search`, `check`, `close`, `key`, `clock` and
  `alert` degraded into boxy stairs; `sev-low` and `status-done` became the same
  square with a stair glyph; solid cells break the stroke-only rule and make
  forced-colors and thin-stroke rendering inconsistent.
- **C. Scanline** (round monoline with a 45 degree intercept gap cut into one
  stroke of each icon, mocked with a mask). Rejected: the gap reads as a rendering
  glitch at 16px, it breaks one-stroke glyphs (`check`, `plus`, `chevron`
  split in two), needs masks or hand-split paths in every symbol, and the
  underlying round monoline is still the stock look.

The four weak icons of the previous set are fixed: `intruder` is a source node
fanning three lanes into a gate bar (no arrowhead), `codec` is a transform chip
with pins, `appearance` is a hatched half octagon, and `sev-low` (down pentagon
with a dash) is a different silhouette from `status-done` (octagon with a tick).

## Vocabulary by domain

- **Workspaces.** `proxy`, `intercept` (hold gate), `repeater` (loop), `intruder`
  (node fanning into a gate), `scanner` (radar), `map` (site tree), `finding` (flag),
  `notes` (notebook), `activity` (pulse), `settings` (sliders), `report`
  (document), `folder` (project).
- **Engagement context.** `scope` (corner brackets around a target), `target`
  (crosshair), `evidence` (paperclip on flow rows), `readiness` (dial), `stop`
  (blockers), `info`.
- **Status and severity.** `status-todo`, `status-done`, `check`, `alert`
  (high, up pentagon), `alert-circle` (medium, octagon), `sev-critical` (diamond), `sev-low` (down pentagon), `info`
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
  `keyboard`, `rocket`, `sun`, `moon`, `appearance`, `intent`, `storage`, `bell`,
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
