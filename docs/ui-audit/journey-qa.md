# Menu and user-journey QA — 2026-09-07

The later [context-menu, color and motion follow-up](context-menu-motion.md)
covers subsequent runtime changes. This review retains its original identity.

This review exercises the application in disposable projects with generic data.
The [manifest](journey-qa/manifest.json) records the runtime, executed probes,
reports, and retained screenshot/download hashes. The earlier
[Findings workspace review](findings-workspace.md) retains its original identity.
The reviewed runtime is
`f3d5c07378696b8fd459afceba6ca4cfda3315fa00fee04bd773a4f95fea9218`
across 257 source files.

## Fixes

- Notes could show Preview while Edit remained selected when a delayed save
  completed. The view now renders its local draft immediately; persistence and
  error/retry feedback continue independently. The [baseline observation](journey-qa/reports/baseline-notes-race.json)
  records the original mismatch.
- Report downloads explicitly opened a native Save picker on supported browsers.
  The shared helper now uses the ordinary download flow. Browser tests measure
  picker non-access and validate exported content, not only filenames.
- Switching projects could reload the page after a failed Notes save and discard
  the draft. Features now guard their unsaved work before the switch request.
  Notes, all Finding drafts, History notes and persisted Settings fields block
  switching until saved. The Projects dialog prevents additional edits during
  reconnection and restores its controls if switching fails. Cmd/Ctrl+K can no
  longer open the command palette over an active dialog. A dialog shell remains
  focusable when every control becomes disabled, keeping Tab inside it.

## Menu coverage

The [panel matrix](journey-qa/reports/matrix.json) passes 232 checks across
Chromium, Firefox, and WebKit at 1440×900, 1024×768, and 390×844. These are
navigation/rendering checks, including empty states, rather than 232 distinct
completed workflows. The connected cases below verify actions and outcomes.

| Menu | Connected coverage and limits |
| --- | --- |
| Proxy / History | Generic loopback capture, inspection, flow-note recovery, saved views, response comparison, and handoff to Repeater. |
| Intercept | Mocked held-request selection and editor content; forwarding and dropping live held traffic were not exercised. |
| Repeater | Actual ordinary local echo send, response/history, persisted editor draft, and unsent Postman import. |
| Intruder | Modes, numeric controls, template/payload layout, history drawer and mobile reachability; no attack was started. |
| Scanner | Target/control layout, Checks/Codecs entries and Code/Docs views; no scan or custom script was executed. |
| Map | Tree/Table/Graph/Params, search/filter controls, generic captured flow → inspector → History. |
| Findings | Creation, four-section editing, tags/status/confidence, captured HTTP and generic PNG attachment, reload/routes, filters/readiness, rejected-write recovery, and exports. |
| Notes | Edit/Preview, reload persistence, delayed-save view ownership, rejected save and retry. |
| Activity | Expansion/navigation surfaces and mocked activity identifying a generic captured flow. |
| Settings | All eight sections; safe scope/capture preferences and data import/export workflows. Operational limits are explicit below. |

All Settings sections—Proxy & network, TLS / CA, Mobile devices, Target scope,
Scanner & OOB, Session / auth, Project & data, and API & MCP—were opened across
the engine/viewport matrix. Custom dropdowns, compact navigation, disclosures,
and secondary API tabs have additional interaction coverage in the
[secondary-view report](journey-qa/reports/secondary.json).

The [Findings journey report](journey-qa/reports/findings.json) records eight
grouped scenarios. Chromium verifies copied HTTP text against the clipboard,
parses JSON exports and checks generic finding/evidence fields, and checks
Markdown/HTML content. Firefox and WebKit perform actual JSON downloads from
the phone layout. The generic PNG tests attachment processing through a file
input change; they do not validate the operating-system file chooser.

The connected workspace evidence is retained separately:

| Report | Verified outcome |
| --- | --- |
| [Local flow](journey-qa/reports/local-flow.json) | Capture → Inspector → Repeater echo/history → persisted draft → Map → History. |
| [Workspace recovery](journey-qa/reports/workspace-supplement.json) | Exact History note after Retry/reload, saved-view filter restoration, unsent Postman request after reload, and correct mocked Activity/held-request selection. |
| [Import and preference checks](journey-qa/reports/strengthened.json) | Imported flow counts/fields survive reload; scope and capture preferences persist; rejected writes recover; Compare IDs and Decoder output agree with inputs. |
| [Notes](journey-qa/reports/notes.json) | Seven grouped checks including delayed Preview → Edit, failed save, reload and custom phone navigation. |
| [Project draft guard](journey-qa/reports/project-draft-guard.json) | Three grouped workflows: failed Notes draft blocks POST, Settings navigation stays usable, rejected/accepted mocked switches recover or wait for the correct identity. The six report entries include paired wrappers, not six unique journeys. |
| [Finding draft guard](journey-qa/reports/finding-draft-guard.json) | An unselected failed draft blocks switching, remains editable and saves after Retry. |
| [Pending switch](journey-qa/reports/pending-switch.json) | Twelve real Tab presses stay in the dialog; Cmd/Ctrl+K and background pointer/typing cannot bypass it. |

## Dialogs and visual behavior

The [dialog matrix](journey-qa/reports/popup-shells.json) passes 486 direct-shell
checks: 18 dialogs × three engines × three sizes × bounds, Tab containment,
and Escape/focus return. Direct shell checks are distinct from the
[feature-entry/disclosure checks](journey-qa/reports/popup-entries.json), which
include 27 actual button/keyboard entries across the engines for guide, export,
new finding, Checks, Codecs, Projects, shortcuts, setup, and image viewing.

Six conditional upstream/rule-pack disclosure paths initially lacked the right
fixture context; all pass in the [fresh-context follow-up](journey-qa/reports/popup-conditional.json).
One limitation remains: the macOS WebKit automation profile skips buttons during
ordinary Tab traversal. The alternate command-palette shortcut entry passes;
this does not replace a physical keyboard or assistive-technology review.

## Scope of the evidence

Failures are injected only into the isolated browser/candidate. Real persistence
and downloads are distinguished from mock Activity/Intercept reads and rejected
writes. Expected HTTP errors are retained separately from unhandled JavaScript
errors. Source, probe, and image identities are never reassigned to an older run.
The retained selection contains 62 PNGs, including four explicitly historical
failing observations; current reports keep hashes for the wider capture set.
Screenshots containing machine discovery or temporary project paths are omitted
from the retained selection; the matrix preserves their capture hashes.

Device setup, OS proxy/listener changes, TLS installation, live session macros,
scanner/Intruder/Authz execution, OOB callbacks, credential creation, sharing,
peer/vault operations, retention deletion, and real project restart/switching
remain operational gaps. The managed candidate deliberately disables project
switching. Menu visibility and disabled-state checks do not prove these actions
work with real hardware, credentials, services, or engagement traffic.

Managed audit binaries use the source fallback version displayed in screenshots.
The separately built local CLI is stamped `2.0.10-local`; screenshot versions are
not relabelled to imply they came from that linker-stamped binary.

## Validation and local installation

`go test ./...`, `go test -race ./...`, `go vet ./...`, all 24 JavaScript
syntax checks, the generated-documentation check, and the no-cgo `make build`
pass. The evidence gate verifies the runtime digest and every retained file hash.
Generated site copies were refreshed from the updated source documentation.

The local CLI was installed atomically with the previous executable preserved.
`interseptor version` reports `interseptor v2.0.10-local`; binary SHA-256 is
`4a3bf43cd79d1fb2ae60ddb2ddfbc70f2d6f6f21ae99a7d4d44f357b4adb1d87`.
All owned audit candidates were stopped and their disposable runtime roots
removed after evidence collection. No release or repository publication was made.
