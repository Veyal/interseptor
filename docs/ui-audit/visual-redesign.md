# Visual redesign — 2026-09-06

Historical review. The later [Findings workspace and shared-surface review](findings-workspace.md)
owns the current redesign and its new verification evidence.

The candidate uses a neutral workbench theme, a consistent original SVG feature
family, clearer Settings hierarchy, and brief surface entrances. Existing custom
controls, keyboard interaction, data ownership, and essential warnings remain.
The design is implemented in the application, not only in mockups.

## Feature coverage

Every row below was source-reviewed. The ten panels and eight Settings sections
were rendered at 1440×900 and 390×844 in Chromium, Firefox, and WebKit. This is
UI-only verification; no security operation was executed to produce the review.

| Surface | Visual / usability change | Additional coverage |
| --- | --- | --- |
| History | New feature identity, calmer search/filter surfaces, compact welcome with Connection settings and HTTPS setup | Both welcome routes; generic populated history with matching healthy diagnosis |
| Intercept | Matching navigation icon, queue/pane surfaces, custom controls | Default presentation only |
| Repeater | Matching navigation icon, calmer request tabs/editor surfaces, vector response placeholder | No sending or replay |
| Intruder | Matching navigation icon, shared controls and results placeholder styling | No active runs |
| Scanner | Matching navigation icon, quieter issue-list and detail surfaces | No scanning |
| Map | Matching navigation icon, unified toolbar and view-control surfaces | No discovery operations |
| Findings | Matching navigation icon, flatter detail canvas, quieter metadata surfaces and dialogs | Generic populated read/edit at top and evidence positions; mobile scroll reachability |
| Notes | Matching navigation icon, quieter editor, increased writing line height and padding | Generic populated note |
| Activity | Matching navigation icon, readable event spacing, concise empty-state guidance | Default presentation |
| Settings: Proxy / network | Section identity, clear form hierarchy and spacing | Desktop/mobile custom section navigation |
| Settings: TLS / CA | Section identity and shared field hierarchy; consequences stay visible | HTTPS welcome route |
| Settings: Mobile devices | Section identity and shared surfaces | Read-only availability; no device setup |
| Settings: Target scope | Section identity and consistent controls | Presentation only |
| Settings: Scanner / OOB | Section identity and consistent controls | Read-only OOB display; no callback operations |
| Settings: Session / auth | Section identity and consistent controls | No authentication execution |
| Settings: Project / data | Section identity, consistent fields and data surfaces | Prior recovery behavior retained |
| Settings: API / MCP | Section identity; optional connection help, alternate client config, and tool list in disclosures | All five tabs; keyboard disclosures and static config Copy; non-ready help defaults open |
| Decoder / Checks / Codecs | Shared modal surfaces, headings, controls, and entrance motion | Desktop/mobile dialogs; no custom-code execution |
| Palette / shortcuts / guide | Shared modal surface and typography | Keyboard and bounds checks |
| Login | Revised entry copy and inherited neutral theme | GET render only, desktop/mobile dark/light; no credentials submitted |
| Comparer / WebSocket / setup dialogs | Shared control/surface styles | Source-reviewed; no send, replay, setup, or operational claim |

## Design decisions

- Keep the app self-contained: no font CDN, new production dependency, raster
  decoration, or video playback. Original vector feature icons stay sharp at
  navigation and empty-state sizes and inherit both themes.
- Reserve mint for identity and selection, deeper emerald for filled controls,
  and severity colors for meaning. A new regression checks the actual badge
  recipes as well as theme tokens; the initially observed 1.51:1 badge contrast
  failed before correcting the fill.
- Proportional type belongs to navigation, headings, and prose. Raw protocol
  data and editors retain monospace where it supports scanning or editing.
- Keep motion short and state-driven. Modal and Settings section entrances use
  the shared duration/easing tokens; reduced-motion disables them.
- Keep optional explanations in accessible disclosures. Do not hide warning,
  save, or recovery states to make screenshots look cleaner.

## Evidence and validation

Current release-prep runtime: `c4a3f82fe6001fa5ac3e690058cb13960310eec7538ab3f2f2dcb5d447c15b36`
across 253 runtime files.

- [Three-engine release matrix](redesign/release-v2.0.10-report.json): 117
  captures against the release-prep source, all ten panels and eight Settings
  sections at both sizes. No page or console errors, external requests,
  viewport overflow, or visible native selects were observed.
- [Earlier three-engine matrix](redesign/matrix/report.json): 117 captures, all ten
  panels and eight Settings sections at both sizes, with explicit dark/light
  views. No page or console errors, external requests, viewport overflow, or
  visible native selects were observed. `nav_gap: 6px` is the intended space
  between rail groups, not the icon-to-label gap.
- [Secondary checks](redesign/secondary/report.json): 26 captures covering
  generic History/Notes/Findings fixtures, secondary dialogs, all API tabs,
  keyboard MCP disclosures, and settled dark controls. The original populated
  History fixture retained the empty managed instance's diagnostic banner;
  it is a diagnostic variant rather than a coherent populated overview.
- [Composition addendum](redesign/composition/report.json): six captures with
  a matching healthy History diagnosis fixture and top-of-Findings read/edit
  views at both sizes. Real warnings were not hidden in product code.
- [Login, welcome routes, and reduced motion](redesign/followup/report.json):
  four login screenshots and explicit checks of both History setup routes and
  computed animation/transition suppression.
- [Capture manifest](redesign/manifest.json): hashes of all 153 final PNGs;
  75 representative PNGs retained here. The full raw matrix remains in the local
  audit output. Parent recomputed source identity and verified copied bytes.
- Local before/after gallery QA passed at both sizes: all 75 retained images load,
  keyboard filters show 39 desktop and 36 mobile captures, and no overflow.
- Pure-Go build, `go vet ./...`, JavaScript syntax for all 22 modules,
  documentation consistency, focused UI regressions, and diff checks pass.
- Full Go and race suites still fail on `TestRunUpdateCheckOnly` (the existing
  release lookup) and `TestUIBrowserAuditEvidenceMatchesCurrentRuntime` (the
  historical operational audit describes an earlier runtime). No other failing
  tests or race warnings were observed. Their evidence has not been relabeled
  to make a release gate pass.

The original full audit and the preceding usability review are historical.
This pass establishes visual and UI interaction evidence, not release or
operational security certification. Commit and push remain pending.
