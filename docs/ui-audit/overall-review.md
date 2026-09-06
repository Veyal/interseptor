# Overall UI and UX review — 2026-09-06

Historical usability pass. The subsequent [visual redesign](visual-redesign.md)
supersedes this pass's runtime identity and screenshots for the current candidate.

This pass covers every main panel, all eight Settings sections, and the secondary
tools and shared dialogs. It evaluates presentation, discoverability, editing,
navigation, and recovery. Source review and browser checks are distinguished;
UI fixtures do not establish operational correctness of security features.

## Implemented improvements

- Mobile uses one custom tool selector, with direct access to the existing
  project dialog. Desktop retains its navigation rail. Workspace startup
  warnings and Reload stay in a shared region outside the hidden mobile rail.
- Settings uses a custom section selector on narrow screens. Search filters
  the available sections, including a disabled no-match state. Desktop keeps
  grouped section navigation. The navigation landmark has an accessible name.
- Listener port fields use the shared input styling.
- Per-request History notes retain failed drafts when switching requests.
  Unsaved, saving, error, and retry states are visible beside the editor.
  Drafts are held in memory for the current app session, not persisted across
  a reload or project switch. Mobile History can scroll when its banners and
  inspector exceed the viewport; request rows and Retry remain reachable.
- Findings evidence editing controls stay visible without hover. The header
  scrolls with content on phones and uses an opaque sticky surface on desktop,
  preventing report text from showing through it.
- Storage statistics preserve existing rows when refresh fails and expose a
  Retry action. Decoder status is announced to assistive technology.
- Project folder input uses a platform-neutral placeholder.

## Source coverage

| Feature | Surfaces reviewed | Outcome and remaining opportunity |
| --- | --- | --- |
| Proxy / History | Search and filters, saved views/searches, columns, selection, pagination, inspector, request notes | Strong virtualization and stale-load handling. Fixed note draft recovery. Consolidate stacked toolbars after checking which filters users use most. |
| Intercept | Queue, filter, request/response editor, empty states, Match & Replace | Existing selection and action ownership guards are substantial. Preserve state and consequence labels when shortening text. |
| Repeater | Request tabs/history, import, request editor, response views | Existing late-response ownership is strong. Response loading errors need inline Retry and announcement. |
| Intruder | Tabs, target, modes, list/payload fields, advanced options, results | Source contains narrow-screen wrapping and result states. Dense advanced configuration needs visual confirmation; no active runs are part of this review. |
| Scanner | Targets, lifecycle display, issue list/detail, Findings handoff | Existing stale-load and lifecycle guards are strong. Review density of issue/detail view on narrow screens; no scans executed. |
| Map | Domain/search/scope, tree/table/graph/parameters, clustering, graph controls, empty/error states | Useful no-match, truncation, and density messages. Host focus remains hard to discover via double-click/F; provide a visible touch-accessible action. |
| Findings | List/filter, creation, guide, read/edit, readiness, evidence blocks, picker, export options, save states | Fixed hover-dependent edit controls and obscured content beneath the mobile header. Further reduce nested padding while preserving evidence warnings. |
| Notes | Editor/preview, autosave, load/save errors, retry, image paste | Existing explicit save/error state is a useful pattern for other editors. |
| Activity | Feed, unread badge, load/error handling | Retain short event labels and recovery feedback. |
| Settings: Proxy / network | Listeners, device endpoint, control listener, upstream, system proxy, capture policy | Fixed listener input styling and navigation overhead. Explicit local save states remain a priority. |
| Settings: TLS / CA | Trust/setup help, verification, bypass and exception lists, diagnostics | Essential verification consequences remain visible; optional setup help is expandable. |
| Settings: Mobile devices | Android, iOS, SSH setup, availability and readiness states | Android disappears when ADB is unavailable; a compact unavailable/setup state would be clearer. Device actions were not executed. |
| Settings: Target scope | Hosts and scope controls | Keep consequences visible and distinguish immediate changes from explicitly saved edits. |
| Settings: Scanner / OOB | Configuration, availability, provider/setup status | Retain prerequisite and unavailable-state explanations. No callback or scan operations executed. |
| Settings: Session / auth | Global/host headers, token/login macro configuration, save and expiry states | Clearer per-form unsaved/saving/saved/error feedback remains needed. No authentication workflows executed. |
| Settings: Project / data | Project selection/create dialog, paths, storage statistics, retention policy and selection | Fixed storage refresh recovery and mobile project entry. Destructive controls keep their explicit consequences. |
| Settings: API / MCP | Key list, access settings, references and service status | Existing retry states reviewed. Reference text stays expandable. No credentials generated. |
| WebSocket | Captured frame inspection and editor surface | Keyboard frame selection exists; sending/replay is outside this UI-only verification. |
| Comparer | Flow modal compare sides and loading errors | Plain-text errors should become scoped, announced errors with Retry. |
| Decoder | Input/output, transformations, chaining, pending/error display | Added live status semantics. |
| Checks / Codecs / Scripts | Scanner tools, custom editor dialogs, saved History search editor | Review includes layout, forms, empty/error states; execution is excluded. |
| Shared UI | Seventeen static dialogs, command palette, context menus, dropdowns, tooltips, themes, motion | Existing modal focus/keyboard controls are substantial. Optional help on disabled controls needs adjacent/tappable explanations; tooltips alone are insufficient. |

## Next priorities

1. Consistent Settings save feedback, tied to actual server acknowledgement and
   existing edit generations. Avoid a generic “Saved” indicator that could
   hide another unsaved form in the same section.
2. Inline Retry for comparison and response-view errors, preserving tab/modal
   ownership while requests are pending.
3. Further mobile density reduction in History and Findings, guided by the
   new screenshots rather than removing necessary status or evidence labels.
4. Tap-accessible optional help and visible reasons beside disabled actions.
5. Unavailable-device guidance and discoverable Map host-focus controls.
6. Bound the existing wait for pending History note saves before reloading an
   inspector. An indefinitely stalled PUT can currently delay returning to that
   request; preserving server-read ordering and local draft ownership needs a
   dedicated follow-up.

## Validation status

- [Main browser matrix](overall/coverage.json): all ten panels and eight Settings
  sections at 1440×900 and 390×844 in Chromium, Firefox, and WebKit. Retained 114
  screenshots, including project-dialog and light-theme views. No unexpected
  page/console errors, external browser requests, viewport overflow, or visible
  native select elements. These are primarily empty/default states.
- [History note recovery](overall/recovery/note-pointer-geometry-recovery.json): mocked save
  failure, flow switching, retained draft, and successful Retry pass. A real
  wheel gesture exposes the mobile Retry button, including with a long
  diagnostic banner; no programmatic scrolling is used to establish reachability.
- [Storage and startup recovery](overall/recovery/recovery-geometry-check.json): mocked
  statistics refresh failure preserves rows and Retry recovers; aborted module
  startup exposes a visible, keyboard-focused mobile Reload button.
- [Secondary UI checks](overall/secondary/report.json): Chromium at both
  viewports covers Decoder, Checks, Codecs, OOB display, writing guide, command
  palette, shortcuts, all API/MCP tabs, populated mocked Findings editing, and
  settled dark-theme controls. Twenty-two screenshots are retained. Every
  evidence-control group is checked after scrolling it into view; the mobile
  header no longer overlaps the evidence text. Compare, WebSocket sending,
  device actions, and other active operations remain source-reviewed only.
- Exact current runtime: `e55b744e3fab1b222c3922399aeb2590d32101fd41184dc5171ba13dfaaaa294`,
  253 runtime files. Screenshot byte hashes are retained with the main matrix.
- Pure-Go build, `go vet ./...`, all UI JavaScript syntax checks, documentation
  consistency, and focused behavior regressions pass. New behavior tests were
  observed failing before implementation. The note race test also proves that
  an acknowledgement for A leaves newer draft B unsaved and B is saved next.
- `go test ./...` and `go test -race ./...` remain blocked by the existing live
  `TestRunUpdateCheckOnly` lookup (`release v2.0.8 not found`) and the historical
  full-audit source-identity mismatch. No other test failures or race warnings
  remain in the final runs.

The earlier full 132-case audit is historical and its source-identity test
correctly rejects this changed runtime. Its digest has not been replaced with
UI-only evidence. This review is not release certification.
