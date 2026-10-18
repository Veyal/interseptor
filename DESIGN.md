# Interseptor website design

The website at `https://veyal.github.io/interseptor/` shares its design language with the main site,
[veyal](https://veyal.github.io): Kirby's Dream Land, grown up. Kirby art carries the personality
and the layout stays disciplined. The landing page plays once, and the docs stay calm. Every page
links back to veyal from the header, the drawer and the footer, marked with a Warp Star.

Design read: a product landing page and documentation for pentesters and agent-driven security
testing, in a soft pastel language that stays legible and credible.

## 1. Visual theme and atmosphere

A pink-tinted Cloud ground, Blush bands, puffy shapes and Plum Ink text. Hierarchy comes from bands,
not outlines: Sky for the landing hero, Dream Night for the product recording, code and the footer,
Blush and Cloud for reading. Surfaces have a 1px hairline at most; the Kirby art has the outlines.
There is one playful moment per page:

- On the landing page, Kirby inhales a request into the hold tray (inhale pose, then cheeks-full).
  Forward makes Kirby spit it out as a Warp Star flying right. Drop makes Kirby swallow it.
- On docs, nothing moves unless you touch it.

Game vernacular is used only where it maps to something true ("inhale" is intercept, "copy ability"
is capability). There are no game levels or start prompts. The site does not carry
the author's name beyond the "veyal" handle.

## 2. Color palette and roles

All values are defined once in `website/assets/site.css`. Components use the semantic tokens, never
raw hex. Ratios are WCAG 2.x, computed from the hex values.

| Token | Hex | Role | Contrast |
|---|---|---|---|
| `--cloud` / `--bg` | Cloud `#FFF8FB` | page ground | Plum Ink 14.4:1, Dusk 6.7:1 |
| `--blush` / `--bg-band`, `--muted`, `--card-alt` | Blush `#FFE6F0` | bands, hover washes, inline code, zebra rows | Plum 12.8:1, Dusk 5.9:1 |
| `--card` | Card `#FFFFFF` | panels, inputs, tables | Plum 15.1:1, Dusk 7.0:1 |
| `--fg`, `--plum` | Plum Ink `#2A2140` | text, focus ring on light | see above |
| `--fg-muted`, `--dusk` | Dusk `#5E5474` | secondary text | 6.7:1 on Cloud, 4.8:1 on Sky |
| `--candy-pink`, `--kirby-pink` | Kirby Pink `#FF9EC4` | primary fills, current nav | Plum on it 7.9:1 |
| `--cheek` | Cheek `#D93A7A` | link underlines, small accents only | 4.2:1 on Cloud, so never body text |
| `--candy-yellow`, `--warp` | Warp Yellow `#FFD44D` | highlights, stars, focus ring on night | Plum on it 10.6:1 |
| `--candy-sky`, `--sky` | Sky `#A8DCFF` | hero band, notes | Plum on it 10.3:1 |
| `--candy-mint`, `--meadow` | Meadow `#8FD694` (shade `#6CC27A`) | hills, success | Plum on it 8.8:1 |
| `--candy-grape` | Wisp `#C9B6FF` | agents and reference group | Plum on it 9.1:1 |
| `--night` | Dream Night `#221A3A` | product, code and footer bands | Moon 14.7:1 |
| `--night-raised` | Night Raised `#2E2550` | cards and bars on night | Moon 12.5:1 |
| `--night-deep` | Night Deep `#170F2B` | page bands and code in the dark theme | Moon 16.5:1 |
| `--lilac` | Lilac `#C9C0E0` | secondary text on night | 9.5:1 on Dream Night, 8.1:1 on Night Raised |
| `--moon` | Moon `#F4F0FF` | primary text on night | see above |
| `--tint-*` | `#FFE6F0` `#FFF4CC` `#E5F6E6` `#E4F4FF` `#F1EBFF` | callout and group washes | Plum 12:1 or better |

Code tokens on Dream Night: comment `#A99FC4` (6.6:1), string `#8FE3B0` (10.8:1), number `#FFD44D`
(11.6:1), keyword `#FF9EC4` (8.6:1), key `#A8DCFF` (11.2:1), flag `#C9B6FF` (9.1:1). All also pass on
Night Raised (5.6:1 or better).

Lines are derived from the ink: `--hair` (12%) for dividers and panel edges, `--line` (25%) for
secondary button and chip borders, `--line-input` (58%, 3.6:1 on white) for text fields and the search
trigger, so WCAG 1.4.11 holds where a border is the only cue.

The **dark theme** keeps the same system: Dream Night is the ground, Night Raised the cards, Moon the
text, Warp Yellow the focus ring. A `.on-night` band (and the footer, code cards and window bars)
re-themes everything inside it through the same tokens, in both themes.

Navigation groups keep one fill each and also print their name: Start here is pink, Use Interseptor
sky, Extend meadow and Reference wisp.

## 3. Typography

| Role | Font | Size | Weight | Line height | Notes |
|---|---|---|---|---|---|
| Hero | Fredoka | `--text-5xl` 44 to 76px | 650 | 1.02 | landing h1 only, -0.015em |
| Page title | Fredoka | `--text-4xl` 36 to 56px | 650 | 1.04 | one h1 per page, -0.01em |
| Section | Fredoka | `--text-3xl` 28 to 38px | 650 | 1.1 | h2 |
| Subsection | Fredoka | 20px | 600 | 1.25 | h3 |
| Lede | Recursive | 18px | 400 | 1.6 | `--fg-muted` |
| Body (UI) | Recursive | 16px | 400 to 500 | 1.6 | Sans Linear (MONO 0) |
| Body (docs prose) | Recursive | 17px | 400 | 1.7 | 72ch measure |
| Code | Recursive Mono | 0.86em inline, 14px blocks | 400 | 1.65 | `font-variation-settings: 'MONO' 1` |

Both families are self-hosted woff2 under the SIL OFL (`website/assets/fonts`, latin subset, axes
pinned as described in `fonts/README.txt`). They are the documented exception to the system-font
default because they are the brand's voice. Latin is preloaded; other scripts fall back to the system
stack. `font-display: swap` is set, with metric-matched fallbacks. There are no all-caps labels, no
tracked eyebrows and no single-word colour accents inside headlines.

## 4. Components

Class names are stable across both sites:

- **`.card-surface`**: a white panel with a 1px hairline and a 16px radius. **`.card-hover`** lifts it
  2px with a soft shadow, and only on things that are links.
- **`.btn-primary`** (Kirby Pink, Plum Ink text) and **`.btn-ghost`** (1.5px Plum/25% line): pills, 44px
  minimum height. Press is a squish: `transform: scale(0.96)` over 120ms. `.btn-sm` and `.btn-lg` are
  the size variants. One label per intent per page.
- **`.icon-btn`**: a 40px round button (theme, menu, close).
- **`.chip`**: a pill for tags, version and copy buttons. `.chip-pink`, `-yellow`, `-mint`, `-sky` and
  `-grape` are fills with Plum Ink text.
- **`.mono-label`**: a Blush pill in sentence case for a short section label. At most one per section.
- **`.ribbon`**: a small Warp Yellow badge pinned to a corner ("Try it", "Real recording"), unrotated.
- **`.window`**: a 28px-radius frame for screenshots, the tray and the recording. A Dream Night bar
  holds pink, yellow and sky dots and a mono title.
- **`.field-input`**: pill, 1.5px `--line-input` border. Focus is the 3px outline.
- **Code card** (server markup): `div.code-card > div.code-bar (span.code-lang, button.code-copy[hidden]) + pre.code > code.language-x`
  with `span.tok-*` tokens. Dream Night body, Night Raised bar, 16px radius. Long lines scroll inside.
- **Table**: `div.table-wrap[role=region][tabindex=0]` with a Blush header row, Blush zebra rows and a
  1px hairline frame. The header stays sticky inside the scroll region.
- **Callouts**: a blockquote is a Note (sky tint). GitHub alerts render as
  `aside.callout.callout-{note,tip,important,warning,caution}` with a printed title, so colour is
  never the only signal.
- **Stars**: `.stars` holds a few static Warp Yellow stars on a night band. `.stars-twinkle` lets three
  of them twinkle three times under no-preference motion, and never on docs pages.
- **Icons**: lucide-static 0.453.0 (ISC), inlined by the `icon` template function.

### Kirby art

Transparent WebP stickers in `website/assets/img/kirby/`, 640px wide. Use them decoratively
(`alt=""`), at most one per screen.

| File | Where |
|---|---|
| `kirby-idle` | tray at rest, scorecard avatar |
| `kirby-inhale` | tray while pulling a request in |
| `kirby-full` | tray holding a request, and its no-JavaScript view |
| `kirby-spit` | tray while a request is forwarded as a Warp Star |
| `kirby-detective` | search palette empty state, features filter with no match |
| `kirby-book` | Getting started header |
| `kirby-laptop` | For agents header and the landing agents band |
| `kirby-sleep` | 404 |
| `kirby-warpstar` | back to veyal link (header, drawer, footer) and the install band |

## 5. Layout

- 8px grid with a 4px half step (`--space-1` to `--space-10`).
- Containers: `.wrap` (1152px) and `.wrap-wide` (1440px, the docs frame). The gutter is
  `clamp(16px, 4vw, 24px)`.
- Radii: 8 (inline), 12 (inner boxes), 16 (cards, code), 28 (panels, windows, the doc sheet), pill.
- The header is a 64px sticky bar on Cloud with a hairline.
- **Landing** sections, in order: Sky hero with the tray and Meadow hills, the real recording (Dream
  Night, with sparse stars), the workflow (Capture, Replay, Fuzz, Prove, Report, a true sequence so it
  is numbered), capabilities, agents, honest limits, questions, install. Bands alternate Cloud and
  Blush. Section padding is 96px on desktop and 64px on mobile. No stat strip, no gradient, no orb.
- **Docs**: three columns at 1200px and up. The sidebar is 264px, the article sits on a white 28px
  sheet with a 72ch measure, and the outline is 220px. From 960 to 1199px the outline becomes a
  disclosure above the article. Below 960px the sidebar becomes a drawer behind a menu button. Each
  page has a breadcrumb, an h1, a lede, a meta row, the article, and previous and next cards.

## 6. Depth

Hierarchy is bands first. Shadows are soft and ambient (`--shadow-md`, `-lg`, `-xl`) and appear only
on things that float: the tray, the recording, the search palette, hovered cards. `--shadow-xs` and
`-sm` are `none`. There are no offset shadows. Layers are: content at 0, the sticky header at 50, the
drawer and search palette at 100, and the skip link at 1000.

## 7. Do and don't

- DO let the band carry the hierarchy and keep boxes to a hairline, a puffy radius and a wash.
- DON'T add 2px outlines, offset shadows, gradients, glass, orbs, or a stat strip of big numbers.
- DO show the real product: the recording and stills come from the real binary with synthetic
  `*.example.com` data. DON'T build fake screenshots out of divs. The tray is labelled as a toy.
- DO use one playful moment per page. DON'T animate on scroll or fade sections in.
- DO write in plain, specific sentences in sentence case. CTAs say what happens.
- DON'T use filler verbs, em dashes in new UI copy, game levels, or the author's name as a tagline.
- DO let colour carry group identity and also print the group name.

## 8. Responsive and accessibility

- Breakpoints: 390 (base), 640, 960, 1200 and 1440.
- Work at 375px with no horizontal page scroll. Wide tables and code scroll inside their own region.
- Primary actions are at least 44px tall. Smaller controls keep their visual size and get an invisible
  44px hit area on coarse pointers. Nothing is smaller than WCAG 2.5.8's 24px.
- Body text is at least 16px on mobile. Dense data tables may use 13px.
- Focus is always a 3px outline: Plum Ink on light, Warp Yellow on night.
- `prefers-reduced-motion` removes the twinkle, squish, hover lift, smooth scrolling and the tray
  animation. The tray stays fully usable step by step, and no content ships at opacity 0.
- `forced-colors` swaps fills for system borders and hides decorative stars.
- Every interactive element is a real `button` or `a`. Dialogs trap focus and close on Escape.
- WCAG AA contrast for every pairing in section 2, in both themes.

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
  The hold tray is a no-network state machine. Kirby's poses are in `data-pose-*` attributes on the
  tray image, and the suction lines are positioned from the mouth fraction `MOUTH` in `snap()`.
- Features, compare, reference and the 404 page bring `css/pages.css` and `js/pages.js`.
- The compare scoreboard is counted from the matrix against the best other tool, so keep the two in
  step when a row changes.

The build does not use Jekyll. `go run ./tools/docscheck build` renders `docs/*.md` and
`website/pages/*` through `website/templates/` into a static `_site/` that GitHub Actions deploys.
Canonical guides live in `docs/`. The navigation and feature data live in `website/data/`.
