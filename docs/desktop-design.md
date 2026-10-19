# Desktop design system

The desktop app (the native macOS shell and Chrome/Edge `--app` windows) renders the same embedded UI
as the browser, with a desktop skin on top. Navigation and flow are unchanged: the rail lists the same
menus in the same order (Proxy, Intercept, Repeater, Intruder, Collections, Scanner, Map, Findings,
Notes, Activity, Settings) and switching between them works as on the web. What changes is how each
screen looks and how much it shows at rest.

Everything here applies only under `html.desktop-app` (set by `js/desktop.js`). A browser tab on the
control URL keeps the web look. Reference feel: Zed and VS Code (panels, tabs, status bar), Proxyman
and Xcode (native macOS type and controls), Linear and Raycast (quiet surfaces, precise states).

## Principles

1. **Readable first.** UI text is 13px, reading text 14px, code 12.5px. 11px is for meta captions only.
2. **Quiet at rest.** One toolbar row per panel. Secondary controls live in a Filters popover or a
   `⋯` overflow menu. At most one primary (filled) button per view.
3. **One place per fact.** Global state (proxy address, intercept and held count, scope, flow count,
   connection, project, version, TLS warnings) lives in the status bar, not in banners and chips.
4. **One component per job.** One button family, one segmented control, two tab styles (content and
   document), one badge family, one callout. No panel-specific restyles.
5. **Colour carries meaning only.** Neutral text everywhere; colour marks HTTP status class, severity,
   intercept/held state and the accent selection. Methods are neutral.
6. **Weights 400, 500, 600 only.** Sentence case everywhere; no uppercase labels.

## Tokens

Desktop tokens remap the existing token names (`--bg*`, `--fg*`, `--line`, `--accent*`, `--fs-*`,
`--sp-*`, radii) so every panel moves at once, then add the new names below. Dark is the default;
light mirrors it.

| Token | Dark | Light | Use |
|---|---|---|---|
| `--surface-0` | `#16171a` | `#f2f2f4` | Window chrome: title toolbar, rail, status bar, tab strips |
| `--surface-1` | `#1c1d21` | `#ffffff` | Panel content |
| `--surface-2` | `#25262b` | `#ffffff` | Raised: popovers, menus, segmented thumb, secondary buttons |
| `--surface-inset` | `#141518` | `#f7f7f9` | Code, request/response bodies, inputs at rest |
| `--surface-tag` | `#2b2c32` | `#ebebef` | Tag and chip fill (visible on white panels) |
| `--surface-hover` | `rgba(255,255,255,.045)` | `rgba(0,0,0,.04)` | Hover tint |
| `--surface-selected` | `rgba(62,201,142,.13)` | `rgba(26,154,99,.11)` | Selected row / current nav item |
| `--border-subtle` | `rgba(255,255,255,.07)` | `rgba(0,0,0,.08)` | Dividers, control borders |
| `--border-strong` | `rgba(255,255,255,.13)` | `rgba(0,0,0,.15)` | Popovers, focused inputs at rest |
| `--text-primary` | `#ececee` | `#1d1d20` | Body and values |
| `--text-secondary` | `#a4a5ac` | `#5c5d65` | Labels, secondary values, 2xx status |
| `--text-muted` | `#71727a` | `#8d8e96` | Captions, placeholders, column headers |
| `--accent` | `#3ec98e` | `#1a9a63` | Selection marker, primary button, links, focus |
| `--accent-subtle` | `rgba(62,201,142,.14)` | `rgba(26,154,99,.12)` | Count badges, focus halo base |
| `--success` | `#3fb97a` | `#1f8f57` | |
| `--warning` | `#e2a336` | `#a8650a` | 4xx, intercept on, held |
| `--danger` | `#ef6262` | `#c93434` | 5xx, errors, destructive |
| `--info` | `#5aa2f0` | `#1f6fc8` | 3xx, informational |

Severity: critical `--danger`, high `#f08a3e`/`#b9561a`, medium `--warning`, low `--info`,
info `--text-muted`. Shown as an 8px dot plus the word.

Type (system UI font, which is SF Pro on macOS; mono is `ui-monospace, "SF Mono", Menlo, monospace`):

| Token | Size / line-height | Weight | Use |
|---|---|---|---|
| `--text-caption` | 11 / 16 | 400–500 | Meta, timestamps, shortcut hints |
| `--text-ui` | 13 / 18 | 400–500 | Controls, tables, labels (the default) |
| `--text-body` | 14 / 21 | 400 | Reading text: findings, notes, descriptions |
| `--text-title` | 15 / 20 | 600 | Pane and dialog titles |
| `--text-heading` | 20 / 28 | 600 | Page headings (Settings sections, Findings title) |
| `--text-code` | 12.5 / 19 | 400 | Bodies, raw HTTP, table path/host columns |

Size and shape:

| Token | Value | Token | Value |
|---|---|---|---|
| `--control-h-sm` | 24px | `--radius-xs` | 4px (badges) |
| `--control-h` | 28px | `--radius-sm` | 6px (controls) |
| `--row-h` | 28px | `--radius-md` | 8px (popovers, cards) |
| `--toolbar-h` | 40px | `--radius-lg` | 10px (dialogs) |
| `--pane-header-h` | 32px | `--gutter` | 12px |
| `--tabstrip-h` | 34px | `--statusbar-h` | 24px |
| `--titlebar-h` | 38px | `--rail-w` | 184px |

Elevation: three shadows only (popover, dialog, drag) plus the overlay colour.

## States

Each state uses its own channel so they never compete.

| State | Treatment | Applies to |
|---|---|---|
| Current / selected | 2px accent marker with rounded ends on the left edge, inset 4px top and bottom, plus `--surface-selected` | Navigation lists only: rail, Settings sections, Collections tree, Findings list, selected History row |
| Hover | `--surface-hover` only | Rows, menu items, ghost buttons |
| Input focus | Background lifts from `--surface-inset` to `--surface-1`, 1px border turns `color-mix(accent 55%)`, plus a 3px halo of `--accent` at about 18% | Every text input, search field, select trigger and editor |
| Keyboard focus | 2px ring of `--accent` at about 60% with 2px offset, `:focus-visible` only, never on mouse click | Buttons, toggles, tabs, chips |
| Row keyboard focus | Hover tint plus a 1px inset accent edge | Lists navigated with arrow keys |
| Pressed | Fill darkens one step for the press | Buttons |
| Disabled | 45% opacity, no hover | Everything |

No 3px yellow ring and no accent-coloured thick borders anywhere on desktop. High-contrast theme
(`data-theme="hc"`) keeps its own strong focus ring.

## Components

- **Button.** Primary (accent fill, `--text-on-accent`), secondary (`--surface-2` with
  `--border-subtle`), ghost (no fill, hover tint; the default inside toolbars), danger, icon-only
  (28px square ghost). Heights 24/28/32. Label 13px, weight 500.
- **Segmented control.** A `--surface-inset` track; the selected segment is a `--surface-2` thumb with
  a hairline shadow (the macOS segmented look). One style everywhere.
- **Tabs.** Content tabs: text, 2px accent underline on the active tab. Document tabs (Repeater,
  Intruder, Collections requests): Zed style. The strip is `--surface-0`, the active tab is
  `--surface-1` and joins the content below, and the close button shows on hover.
- **Badge.** Count (pill, 11px, weight 600, `--accent-subtle`), status (text only, coloured by class),
  severity (dot plus word), tag (`--surface-2`, `--radius-xs`).
- **Callout.** Inline in the pane: a tinted background with a 1px tinted border, 13px, one line, and
  an optional action and dismiss. No full-width amber banners; global warnings go to the status bar.
- **Pane header.** 32px: title (15px, weight 600) on the left; ghost actions and `⋯` on the right.
- **Table / list row.** 28px, 13px text, host and path in 12.5px mono, a hairline divider at 50%
  opacity. Columns: id muted, method neutral weight 500, status coloured by class, size/time muted
  tabular numbers.
- **Empty state.** A 20px muted icon, one 13px line, and an optional ghost action. Centred in the pane.
- **Popover / menu.** `--surface-2`, `--radius-md`, popover shadow, 28px items, hover tint, current
  item checkmark.

## Shell

- **Title toolbar** (38px, `--surface-0`, continuous with the native title bar colour): the project
  switcher on the left; a centred command field (`⌘K`, 360px, inset look) in the middle; on the right,
  the intercept toggle pill (the only intercept control in the chrome; amber when on, with the held
  count) and the readiness ring. The scope chip, split and close-pane icons, the Live chip and the
  theme toggle leave the bar. They move to the status bar, the View menu and the palette.
  In the native macOS app the toolbar *is* the title bar (one row): the traffic lights sit at its
  left, the project switcher starts after them (`--titlebar-inset-left`), the command field stays
  centred on the window, and empty toolbar space drags the window (double-click zooms).
- **Rail** (184px, `--surface-0`): the same items, order and groups. Items are 30px at 13px weight 500,
  with an 18px icon. Group labels are 11px muted sentence case with 12px above. The current item uses
  the current/selected state. Badges use the count style, only for Intercept (held) and Findings.
- **Status bar** (24px, `--surface-0`, 11px): on the left, proxy dot and address, intercept state and
  held count, scope summary ("Scope 1 in · 1 out"), and flow count. On the right, a TLS/issues warning
  icon when relevant (opens a popover), connection dot, project and version. Every item is a button
  that opens the relevant popover or panel.

## Panels

Each panel keeps its features and flow; the redesign changes how they are arranged:

- Exactly one toolbar row (`--toolbar-h`) above content: the main search or filter on the left, then
  filter pills, then right-aligned ghost actions and `⋯`.
- Active filters render as removable tokens inside the search field, not as a separate chip row.
- Banners become inline callouts or status bar items.
- View switchers with more than four options keep the top three visible and move the rest to a
  "More" dropdown.
- Proxy, Repeater, Intruder and Intercept message views share one inspector look: content tabs, a
  `--surface-inset` code area, and an action row only when it has actions.
