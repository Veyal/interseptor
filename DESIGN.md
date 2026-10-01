# Interseptor documentation design

The documentation uses the application's neutral surfaces, green accent, traffic mark, and system
font stacks. It is a reading and navigation surface: concise introductions, visible page location,
and direct links to the next useful guide.

## Color and type

`website/assets/site.css` owns the documentation tokens. Light and dark themes each define their
own foreground, muted text, surfaces, borders, and accent. Green identifies links and selected
navigation. Body text uses Inter when installed, then the system sans-serif stack; code and release
metadata use the local monospace stack. No third-party fonts or images are requested.

Article headings are compact sans-serif. Prose stays near 75 characters per line. Code blocks and
wide tables scroll within the reading column rather than widening the document.

## Layout

The desktop shell has one documentation sidebar, one reading column, and an optional page outline.
The header contains search, release information, source access, and the theme control. The current
page is marked visually and with `aria-current`.

Below 960px, a custom menu button reveals navigation in the document flow. Below 1200px, the page
outline becomes an explicit disclosure above the article. Without JavaScript, navigation remains
visible and all documentation links still work.

The homepage provides three common starting points, current release context, and a short guide
directory. Avoid repeated slogans, decorative terminals, testimonial cards, gradients, and invented
product screenshots.

## Interaction

Search loads its generated index on demand, ranks title matches first, and reports loading,
no-result, and retry states. Results are ordinary links with real keyboard focus. Slash or
Ctrl/Command+K focuses search; arrows move through results and Escape dismisses them.

Buttons, links, menu reveals, and search results use short transitions. Reduced motion disables
animation and smooth scrolling. There are no continuous decorative animations, native select menus,
or browser alert/confirmation dialogs.

## Content and validation

Canonical guides live in `docs/`; `go run ./tools/docscheck generate` produces their public Markdown
pages, the search index, and release metadata. `_data/navigation.yml` owns the sidebar and
`_data/features.yml` maps each canonical feature to a published guide. The release badge comes from
the first published entry in `CHANGELOG.md`, never the Unreleased heading.

Validate the real Jekyll output for links, canonical URLs, small-screen layout, both themes, keyboard
navigation, search recovery, and reduced motion. The Pages build source URL is the origin
`https://veyal.github.io`; `/interseptor` belongs only in `baseurl`.
