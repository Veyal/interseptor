# Findings workspace and shared surfaces — 2026-09-07

This revision replaces the report-like Findings pane with a searchable browser
workspace. The earlier [visual redesign](visual-redesign.md) remains historical.
The newer [menu and journey QA](journey-qa.md) covers the subsequent Notes and
download fixes; the verification record below remains bound to its original runtime.

## Reading and editing

- Search titles, summaries, targets, tags and IDs; narrow the list by severity,
  status or tag. An open record remains available when filters exclude it,
  with an explanation and Clear filters action.
- Overview, Evidence, Remediation and Review have real links. Browser history,
  shared links and opening a link in another tab identify the finding and
  section. A missing finding shows a missing-record state.
- The reader keeps its navigation visible while the selected section scrolls.
  Technical metadata and destructive actions are collapsed. The phone layout
  switches between the list and reader and provides an explicit Back action.
- Evidence has a step outline, expandable captured Request/Response text,
  copy actions and a full inspector. Screenshot evidence opens in the existing
  viewer. Binary and oversized bodies offer a download instead of a text render.
- Editing uses the same four sections. Readiness gaps open the relevant editor.
  Export options live in a dedicated dialog.
- Rejected writes keep the newest field or evidence draft in this tab, tied to
  its original finding. Done keeps the editor open until recovery, and inline
  Retry resubmits through the existing serialized write queue. Switching
  findings and live refreshes preserve these drafts; they are session-local.

## Shared controls

Custom dropdowns and the History column picker fit the visual viewport and can
open above their trigger. The column picker preserves checkbox focus and closes
with Escape. Context menus expose their trigger state. Dialogs and HTTP panes
fit small screens; the mobile flow inspector uses Request/Response controls and
opens on Request when no response is available.

Disclosures share a chevron and short motion, with reduced-motion support.
Tooltips avoid touch-hover obstruction. Toasts have bounded stacking and height.
Activity rows without a captured flow expand to show their full detail.
Settings textareas use the same clear field boundaries as other form inputs.
The stacked Intruder layout reserves a readable request editor and keeps its
results reachable through the workspace scroll area.

## Verification record

The [evidence manifest](findings-workspace/manifest.json) binds the reports,
executed browser probes and retained PNGs to the embedded runtime source.
The final runtime digest is
`907c942a4eef7e8a70cf3477c3b66d89a21110256bf43f86883a272bf39ffc7b`
across 257 files. Earlier screenshots retain their original identities.

| Surface | Review coverage |
| --- | --- |
| Proxy, Intercept, Repeater, Intruder, Scanner, Map, Findings, Notes, Activity, Settings | Visible panel state, document bounds and custom select rendering in Chromium, Firefox and WebKit at 1440×900, 1024×768 and 390×844. The matrix includes empty states; Findings has separate populated captures. |
| Proxy/network, TLS, Devices, Scope, Scanner/OOB, Session/auth, Project/data, API/MCP settings | Each section rendered at all nine engine/viewport combinations; disclosures and custom controls exercised separately. Device screenshots use generic discovery responses. |
| Findings | Four reading sections, evidence editing, inline HTTP, full inspector, export dialog, image viewer, filters, routes, refresh and failed-save recovery. |
| Shared dialogs | All 18 registered shells checked for settled bounds, Tab containment, Escape and focus return. Actual entry checks are identified separately from direct calls to the shared modal helper. |
| Shared controls | Disclosures, custom dropdowns, column picker, tooltips, toast bounds, readiness-link focus and History splitter size feedback. |
| Compact layouts | Phone Findings list/reader navigation and HTTP sides; Intruder editor height and scrolling to Results; Settings action reachability. |
| Inner views | Map modes/search, Repeater response modes/history, Intruder mode/numeric panels/history, Notes Edit/Preview, API settings tabs, command-palette navigation, Checks and Codecs Code/Docs tabs: Chromium desktop and phone. |
| Theme and motion | Light screenshots at three sizes, representative dark Findings captures and reduced-motion checks in all three engines. |

See the [panel matrix](findings-workspace/reports/matrix.json),
[Findings captures](findings-workspace/reports/visuals.json) and
[save-recovery report](findings-workspace/reports/recovery.json) for individual
results. The recovery probe deliberately rejects local saves with HTTP 500;
those expected errors are recorded separately from unhandled browser errors.
The panel matrix records 232 passing cases and 189 captures. The focused
Findings review adds 72 captures; five save/navigation scenarios pass.
The [mobile editor measurement](findings-workspace/reports/geometry.json)
confirms a 180px editor and scroll access to Results in all three engines.

The [modal matrix](findings-workspace/reports/popup-modal.json) records 486
passing checks. The [control/disclosure review](findings-workspace/reports/popup-broad.json)
records 123 passing checks; its conditional upstream and rule-pack cases are
covered by a [separate fresh-context run](findings-workspace/reports/popup-conditional.json).
The remaining WebKit Tab limitation is described below.
The [secondary-view run](findings-workspace/reports/secondary.json) records
16 passing grouped scenarios on desktop and phone with no browser errors.

Code verification passed: `go test ./...`, `go test -race ./...`, `go vet ./...`,
`CGO_ENABLED=0 go build ./cmd/interseptor`, syntax checks for all 24 JavaScript
modules, and `git diff --check`. The visual evidence test verifies the retained
files and their runtime identity as part of the Go suite.

The macOS WebKit test profile skips buttons during ordinary Tab traversal.
Chromium and Firefox exercise that tooltip keyboard path; the WebKit focus-event
path has a [separate passing probe](findings-workspace/reports/webkit-focus.json),
and alternate command-palette entry is recorded separately. This review
does not substitute for testing with assistive technology or real mobile devices.

Representative current screenshots:

- [Desktop Findings](findings-workspace/screenshots/visuals/chromium-1440x900-overview.png)
  and [phone Evidence](findings-workspace/screenshots/visuals/chromium-390x844-evidence.png).
- [Inline captured HTTP](findings-workspace/screenshots/visuals/chromium-1440x900-inline.png)
  and [phone inspector](findings-workspace/screenshots/visuals/chromium-390x844-http-popup.png).
- [Export dialog](findings-workspace/screenshots/visuals/chromium-390x844-export.png)
  and [Session settings](findings-workspace/screenshots/matrix/chromium-390x844-settings-session.png).
- [Custom dropdown](findings-workspace/screenshots/controls/phone-findings-severity-dropdown.png)
  and [column picker](findings-workspace/screenshots/controls/phone-column-picker.png).

The UI review uses disposable projects, generic example.com records, captured
loopback HTTP and explicitly labelled UI screenshots. Device setup, live
authentication, scanning, replay and other external operations are outside this
visual review; dependent UI states use fixtures where needed.
