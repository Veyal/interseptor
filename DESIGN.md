# Interseptor documentation design

The documentation is the Interseptor wing of [veyal.github.io](https://veyal.github.io) and uses the
same sticker-paper look: cream paper, thick ink outlines, hard offset shadows, and candy accents.
It stays a reading and navigation surface: concise introductions, visible page location, and direct
links to the next useful guide. Every page links back to the main site from the header and footer.

## Tokens

`website/assets/site.css` defines every token once on `:root`; a `:root[data-theme='dark']` block
overrides only the values below. Semantic names are used in components, never raw hex.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg` | Cream Paper `#fff6e8` | Night Ink `#1a1722` | page ground, header |
| `--card` | Card White `#ffffff` | Raised Ink `#272331` | cards, inputs, code, tables |
| `--fg` | Ink `#2b2735` | Cream `#fff6e8` | text |
| `--muted-fg` | Dusk `#645e71` | Lilac Mist `#bdb6ca` | secondary text |
| `--muted` | Oat `#f7efde` | Deep Ink `#211d2a` | zebra rows, inline code, code bar |
| `--edge` | Ink `#2b2735` | Cream `#fff6e8` | 2px outlines and offset shadows |
| `--focus` | Ink `#2b2735` | Candy Yellow `#ffc94d` | focus outline |
| `--on-candy` | Ink `#2b2735` | Ink `#2b2735` | text on candy fills, never themed |
| `--candy-pink` | Bubblegum `#ff6fa5` | same | primary button, search focus shadow, link underline |
| `--candy-yellow` | Butter `#ffc94d` | same | labels, current page, hover, table header |
| `--candy-mint` | Mint `#4cd4a9` | same | success and badges |
| `--candy-sky` | Sky `#5ab8ff` | same | note callouts, badges |
| `--candy-grape` | Grape `#a78bfa` | same | badges |

Candy colours are fills only. Text on them is always `--on-candy`; white on pink fails contrast
(2.6:1) and pink text on cream fails (2.4:1), so links are ink with a pink underline instead.
Dark mode keeps the candy fills and inverts the outline colour to cream so stickers keep their edge.
The main site has no dark theme; this one is a documentation decision.

## Type

Headings use Baloo 2 (500 to 800) and body text uses Nunito (600 base, 800 emphasis). This is the
documented exception to the system-font default, and both are self-hosted from
`website/assets/fonts/` as latin and latin-ext woff2 subsets under the SIL OFL (`OFL.txt`), so
the documentation still requests nothing from third parties. Latin is preloaded; latin-ext
downloads only when a page needs it. `font-display: swap` plus metric-matched local fallbacks
(`Nunito Fallback`, `Baloo 2 Fallback`) limit layout shift. Code uses the local monospace stack.
Body copy is 16px; prose stays near 75 characters per line.

## Components

- `.card-surface`: white card, 2px outline, 16px radius, `5px 5px 0` offset shadow, no blur.
  `.card-hover` adds a translate and a larger shadow on hover and focus.
- `.chip`: pill with 2px outline and `2px 2px 0` shadow (back link, release badge, copy button).
- `.btn-primary` (pink) and `.btn-ghost` (white, yellow on hover): 4px shadow, translate on hover
  and press. `.mono-label` is the yellow uppercase pill used for the release context.
- Callouts: a blockquote is a Note (sky). Add `{: .warning}` (yellow) or `{: .responsible-use}`
  (pink) after it, or use `.callout .callout-warning` markup. Every variant prints its own label,
  so colour is never the only signal.
- Code card: bordered card with a language label and a Copy chip (added by `site.js`); long lines
  scroll inside the card. Tables are wrapped in a focusable, scrollable region with a yellow
  header row and `--muted` zebra rows.
- Sidebar: small uppercase section labels; the current page is a yellow pill with an outline
  and `aria-current="page"`.

## Layout

One header (brand badge, search, back link, release chip, source link, theme toggle), one
documentation sidebar, one reading column, an optional page outline, and a full-width footer that
repeats the back link. Below 960px a menu button reveals navigation in the document flow. Below
1200px the page outline becomes a disclosure above the article. Without JavaScript, navigation
stays visible and all links work.

The homepage provides three starting-point cards with candy icon badges, a yellow release label,
and a short guide card grid. Avoid repeated slogans, decorative terminals, testimonial cards,
gradient or orb backgrounds, and invented product screenshots.

## Interaction and accessibility

Search loads its generated index on demand and ranks title matches first. Results are ordinary
links in a bordered card. Slash or Ctrl/Command+K focuses search, arrows move through results, and
Escape dismisses them. Focus is always an outline (3px, offset); the pink `3px 3px 0` shadow on the
search field and results is an addition, never a replacement. Hover and press use transform and
shadow only, and reduced motion disables transitions, animation, smooth scrolling and hover
movement. `forced-colors` mode swaps shadows for system-colour borders. All text and candy
pairings meet WCAG AA (ink on every candy fill is 5.3:1 or better; secondary text is 5.4:1 or better in both themes).

## Content and validation

Canonical guides live in `docs/`; `go run ./tools/docscheck generate` produces their public Markdown
pages, the search index, and release metadata. `_data/navigation.yml` owns the sidebar and
`_data/features.yml` maps each canonical feature to a published guide. The release badge comes from
the first published entry in `CHANGELOG.md`, never the Unreleased heading.

Validate the real Jekyll output for links, canonical URLs, small-screen layout, both themes, keyboard
navigation, search recovery, and reduced motion. The Pages build source URL is the origin
`https://veyal.github.io`; `/interseptor` belongs only in `baseurl`.
