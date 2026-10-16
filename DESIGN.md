# Interseptor website design

The website at `https://veyal.github.io/interseptor/` is the **boss stage of veyal.land**. The main
site, [veyal.github.io](https://veyal.github.io), is a candy-sticker game world (player stats,
collected abilities, a "Boss stage" ribbon on the Interseptor card). This site uses the same
materials and tokens, but turns the volume down where people read. The landing page plays, and the
docs stay calm. Every page links back to veyal.land from the header and the footer.

Design read: a product landing page and documentation for pentesters and agent-driven security
testing, in a candy-sticker game language that stays legible and credible.

## 1. Visual theme and atmosphere

Cream dotted paper, thick ink outlines, hard offset shadows, candy fills, chunky rounded type.
Things look like stickers you could peel off: every surface has a 2px ink edge and a shadow with
no blur. There is one playful, orchestrated moment per page:

- On the landing page, a playable intercept tray.
- On docs, nothing moves unless you touch it.

Game vernacular, such as "boss stage", "stage map", "abilities" and "boss stats", is used where it
maps to something true about the product. It never replaces a plain label a reader needs to find
their way.

## 2. Color palette and roles

All values are defined once in `website/assets/site.css`. Components use the semantic tokens, never
raw hex.

| Token | Light | Dark | Role |
|---|---|---|---|
| `--bg` | Cream Paper `#FFF8EB` | Night Ink `#1A1722` | page ground; veyal `--background` |
| `--bg-band` | Butter Band `#FFE5AC` | Plum Band `#2A2433` | footer and alternating landing bands; veyal footer |
| `--card` | Card White `#FFFFFF` | Raised Ink `#272331` | stickers, docs sheet, inputs, tables |
| `--card-alt` | Butter Cream `#FFF3D6` | `#2F2A3B` | window bars, panels; veyal `--secondary` |
| `--muted` | Oat `#F7EFDE` | Deep Ink `#211D2A` | zebra rows, inline code, hover wash |
| `--fg` | Ink `#2B2735` | Cream `#FFF8EB` | text |
| `--fg-muted` | Dusk `#645E71` | Lilac Mist `#BDB6CA` | secondary text (5.9:1 and 9:1) |
| `--edge` | Ink `#2B2735` | Cream `#FFF8EB` | every 2px outline and hard shadow |
| `--focus` | Ink | Butter `#FFC94D` | focus outline |
| `--on-candy` | Ink | Ink | text on any candy fill, never themed |
| `--candy-pink` | Bubblegum `#FF6FA5` | same | primary action, links underline, "Start here" group |
| `--candy-yellow` | Butter `#FFC94D` | same | labels, ribbons, current page, hover |
| `--candy-mint` | Mint `#4CD4A9` | same | success, "proved", "Extend" group, the mascot |
| `--candy-sky` | Sky `#5AB8FF` | same | notes, info, "Use Interseptor" group |
| `--candy-grape` | Grape `#A78BFA` | same | agents, "Reference" group |
| `--tint-*` | `#FFE3EE` `#FFF3D6` `#DCF7EE` `#E2F1FF` `#EEE8FE` | dark tints | callout and group washes |

Code always uses veyal's terminal card: an Ink body (`--code-bg`) with Cream text and candy tokens.
The token colours are comment `#A9A1BA`, string Mint, number Butter, keyword `#FF8AB8`, key `#7CC6FF`
and flag `#BBA6FF`, each at least 5.9:1 on the code ground.

Candy colours are fills. Ink on every candy fill is 5.3:1 or better. White on pink (2.6:1) and
pink text on cream (2.4:1) fail, so links are ink with a pink underline. Navigation groups keep one
colour each so a reader always knows which part of the docs they are in:

- Start here: pink
- Use Interseptor: sky
- Extend: mint
- Reference: grape

## 3. Typography

| Role | Font | Size | Weight | Line height | Tracking | Notes |
|---|---|---|---|---|---|---|
| Hero | Baloo 2 | `--text-5xl` 44 to 76px | 800 | 1.0 | -0.02em | landing h1 only |
| Page title | Baloo 2 | `--text-4xl` 36 to 56px | 800 | 1.05 | -0.01em | one h1 per page |
| Section | Baloo 2 | `--text-3xl` 28 to 38px | 800 | 1.1 | -0.01em | h2 |
| Subsection | Baloo 2 | 20px | 700 | 1.25 | 0 | h3 |
| Lede | Nunito | 18px | 600 | 1.6 | 0 | `--fg-muted` |
| Body (UI) | Nunito | 16px | 600 | 1.6 | 0 | veyal's body weight |
| Body (docs prose) | Nunito | 17px | 500 | 1.7 | 0 | 72ch measure |
| Label | Nunito | 12px | 800 | 1.3 | 0.08em | uppercase, `.mono-label` only |
| Code | Geist Mono | 0.86em inline, 14px blocks | 500 | 1.65 | 0 | same mono as veyal's terminal |

The three families are self-hosted woff2 under the SIL OFL. They are the documented exception to the
system-font default because they are the brand's voice on veyal.land. Latin is preloaded and
latin-ext loads on demand. `font-display: swap` is set, with metric-matched fallbacks. Uppercase is
reserved for `.mono-label` and ribbons. A headline may carry one pink `.squiggle` underline, which is
veyal's signature move, and nothing else is accented inside headlines.

## 4. Components

Use veyal's class names so both sites read as one system:

- **`.card-surface`**: a white sticker with a 2px ink edge, 16px radius and a `5px 5px 0` shadow.
  **`.card-hover`** lifts it by `translate(-2px,-2px) rotate(-0.4deg)` with an 8px shadow, and only
  on things that are links.
- **`.btn-primary`** (pink) and **`.btn-ghost`** (white, butter on hover): 12px radius, 44px minimum
  height, 4px shadow. Hover lifts 2px and the shadow grows to 6px. Press moves 2px in and the shadow
  shrinks to 1px. `.btn-sm` and `.btn-lg` are the size variants. One label per intent per page.
- **`.icon-btn`**: a 40px square sticker (theme, menu, close). On hover it turns butter and rotates
  -6deg.
- **`.chip`**: a pill with a 2px shadow, for tags, version and copy buttons. `.chip-pink`, `-yellow`,
  `-mint`, `-sky` and `-grape` are fills, like veyal's certification chips.
- **`.mono-label`**: a butter uppercase pill with a 3px shadow, for a short section label. At most
  one per section, and only when it adds information.
- **`.ribbon`**: a rotated butter corner sticker on a positioned parent, such as "Boss stage" or
  "Real recording".
- **`.window`**: veyal's terminal card. A butter-cream bar holds pink, yellow and mint dots and a mono
  title, then the body. It frames every real screenshot, the demo video, the intercept tray and code
  blocks.
- **`.field-input`**: 12px radius, 2px edge. Focus adds the outline plus a `3px 3px 0` pink shadow.
- **Code card** (server markup): `div.code-card > div.code-bar (span.code-lang, button.code-copy[hidden]) + pre.code > code.language-x`
  with `span.tok-*` tokens. It is a window: a butter-cream bar over an ink body. Long lines scroll
  inside the card.
- **Table**: `div.table-wrap[role=region][tabindex=0]` holds a table with a butter header row, Oat
  zebra rows and a 2px outer edge. The header stays sticky inside the scroll region.
- **Callouts**: a blockquote is a Note (sky tint). GitHub alerts render as
  `aside.callout.callout-{note,tip,important,warning,caution}` with a printed title, so colour is
  never the only signal.
- **Mascot**: an original candy-mint frog whose tongue intercepts things mid-flight. The art is in
  `website/assets/img/mascot/frog-{catch,drop,search,agent,boss}-{512,1024}.webp`. It appears in at
  most one place per screen, always decorative (`alt=""`) unless it carries meaning.
- **Icons**: lucide-static 0.453.0 (ISC), the same family as veyal.github.io, inlined by the `icon`
  template function. Use 2.25 stroke and no hand-drawn icon paths.

## 5. Layout

- 8px grid with a 4px half step (`--space-1` to `--space-10`).
- Containers: `.wrap` (1152px, veyal `max-w-6xl`) and `.wrap-wide` (1440px, the docs frame). The
  gutter is `clamp(16px, 4vw, 24px)`.
- Radii: 8 (inline), 12 (controls), 16 (cards and windows), pill.
- The header is a 64px sticky bar on cream with a 2px bottom edge, as on veyal.
- **Landing** sections, in order: hero with the intercept tray, the real recording, the stage map
  (Capture, Replay, Fuzz, Prove, Report, a true sequence so it is numbered), abilities, agents, boss
  stats (the honest limits), questions, install. Alternate cream and butter-band grounds. Section
  padding is 96px on desktop and 64px on mobile. The page uses no stat strip, no gradient and no
  orb.
- **Docs**: three columns at 1200px and up. The sidebar is 264px, the article sits on a white sticker
  sheet with a 72ch measure, and the outline is 220px. From 960 to 1199px the outline becomes a
  disclosure above the article. Below 960px the sidebar becomes a drawer behind a menu button. Each
  page has a breadcrumb, an h1, a lede, a meta row (reading time, last updated, view source), the
  article, and then previous and next cards.

## 6. Depth

Shadows are the only elevation: xs 2px, sm 3px, md 4px, lg 5px and xl 8px, all `0` blur in `--edge`.
Light comes from the top left, so shadows fall to the bottom right. Nothing uses blur except the
sticky header's backdrop, which is veyal's `backdrop-blur-md`. Layers are: doodles at -1, content
at 0, the sticky header at 50, the drawer and search palette at 100, and the skip link at 1000.

## 7. Do and don't

- DO keep every surface on the sticker recipe: 2px edge, radius from the scale, hard shadow.
- DON'T use gradients, glass, blurred shadows, orbs, or a stat strip of big numbers.
- DO show the real product: the recording and stills come from the real binary with synthetic
  `*.example.com` data.
- DON'T build fake screenshots or fake terminals out of divs. The intercept tray is labelled as a toy,
  and it works.
- DO use one bold moment per page. Everything around it stays quiet.
- DON'T animate on scroll or fade each section in.
- DO write in plain, specific sentences in sentence case. CTAs say what happens.
- DON'T use filler verbs, em dashes in new UI copy, or "→" glued onto every link.
- DO let colour carry group identity (pink, sky, mint, grape) and also print the group name.

## 8. Responsive and accessibility

- Breakpoints: 390 (base), 640, 960, 1200 and 1440.
- Work at 375px with no horizontal page scroll. Wide tables and code scroll inside their own region.
- Primary actions are at least 44px tall. Smaller stickers keep their visual size and get an
  invisible 44px hit area on coarse pointers: chips, copy buttons, `.btn-sm`, heading anchors and
  the 40px `.icon-btn`. Nothing is smaller than WCAG 2.5.8's 24px.
- Body text is at least 16px on mobile. Dense data tables may use 13px, which keeps wide rows
  compact without sideways scrolling.
- Focus is always a 3px outline (Butter in dark mode). Shadows are additions, never replacements.
- `prefers-reduced-motion` stops the doodles, hover movement, smooth scrolling and the tray
  animation. The tray stays fully usable step by step.
- `forced-colors` swaps shadows for system borders.
- Every interactive element is a real `button` or `a`. Dialogs trap focus and close on Escape.
- WCAG AA contrast for every pairing listed above, in both themes.

## 9. Agent prompt guide

1. Read this file and `website/assets/site.css` first. Use the tokens and primitives and never
   introduce a new colour, radius, shadow or font. If something is missing, extend it in your own
   stylesheet with the existing tokens.
2. Templates are Go `html/template` files under `website/templates/`, and pages are under
   `website/pages/`. The data contract is in `tools/docscheck/site.go`, and the template functions
   are `url`, `abs`, `asset`, `icon`, `markdown`, `dict`, `add`, `sub`, `json`, `lower` and
   `contains`.
3. Preview with `go run ./tools/docscheck serve -addr 127.0.0.1:<port>`. Validate with
   `go run ./tools/docscheck build <dir> && go run ./tools/docscheck check-site <dir>`.
4. Check every page you touch at 390px and 1440px, in light and dark, with the keyboard only, and
   with reduced motion.
5. Copy names things the way users do. Keep the existing voice: direct, honest about limits, with no
   marketing filler.
6. Use only real product media and synthetic `example.com` data. No private data, ever.

### Shared behaviour contracts

The chrome (`partials/*`, `site.js`, `search.js`) provides these hooks. Pages use them instead of
reimplementing them.

| Hook | Behaviour |
|---|---|
| front matter `css: [css/x.css]`, `js: [js/x.js]` | links a per-page stylesheet or script |
| front matter `sections: [{title, id}]` | adds a "This page" column to the footer |
| front matter `prose: true` | gives an HTML page in the `page` layout the white sheet and `.prose` styles (Markdown pages get them automatically; HTML pages otherwise get `.page-plain`) |
| `html.js` | set before first paint; hide script-only controls with `html:not(.js)` |
| `body[data-page-kind]` | `docs`, `landing`, `page` or `notfound` |
| `[data-color=pink\|sky\|mint\|grape\|yellow]` | sets `--group` and `--group-tint` on that element |
| `button.copy-button[data-copy="#id"]`, `[data-copy-text]` or `[data-copy-url]` | copies text (skipping `[data-copy-skip]`) or a fetched URL, shows "Copied" and announces it |
| `[data-search-open]` with optional `data-search-query` | opens the search palette (also `/` and Cmd/Ctrl+K) |
| `document` event `themechange` with `detail.theme` | fires when the theme toggle changes `html[data-theme]` |

Page structure:
- The landing page lives in `website/pages/index.html`, with `css/landing.css` and `js/landing.js`.
  The hold tray is a no-network state machine. If the frog art changes, the tongue origin is in
  `snap()`.
- Features, compare, reference and the 404 page bring `css/pages.css` and `js/pages.js`.
- The compare scoreboard is counted from the matrix against the best other tool, so keep the two in
  step when a row changes.
- The mascot sources are `website/assets/img/mascot/src/frog-*.svg`. Rasters are rendered from them
  at 512 and 1024.

The build does not use Jekyll. `go run ./tools/docscheck build` renders `docs/*.md` and
`website/pages/*` through `website/templates/` into a static `_site/` that GitHub Actions deploys.
Canonical guides live in `docs/`. `_data` is gone, and the navigation and feature data live in
`website/data/`.
