# UI audit verification — 2026-09-05

> **Superseded runtime:** the overall UI pass on 2026-09-06 changes navigation,
> History note recovery, and Settings recovery. The full 132-case evidence below
> belongs to the earlier runtime and no longer matches the working tree. See
> [the overall review](overall-review.md) for current UI-only coverage and
> validation. This is not a refreshed full release audit.

## Revision and environment

- Audited worktree base: `application_source.worktree_base_commit` in
  [`browser-audit.json`](browser-audit.json), with the exact saved-workspace recovery runtime and
  harness captured by the adjacent digests
- Exact application identity: `application_source.runtime_sha256` in
  [`browser-audit.json`](browser-audit.json), computed from every Git-tracked or nonignored runtime
  file under `cmd/` and `internal/` plus `go.mod` and `go.sum`
- Exact harness identity: `application_source.audit_harness_sha256` binds the retained evidence to
  the precise managed Playwright/CDP script that produced it; `worktree_base_commit` is explicitly
  the clean commit underneath any audited worktree changes, not a claim that the evidence file was
  already committed
- Audited worktree base: `c1ac41af5a3f01736b2291b2067468c2f3accb9c`
- Launch: fresh CGO-free build of the exact audited source, started directly by the managed harness
- Instance guard: full mode built and owned the exact worktree candidate, pre-bound random loopback
  control/proxy listeners, retained them in the parent for the full run, passed those exact
  descriptors to the child without rebinding, created a sentinel-marked disposable OS-temp project,
  and verified version `2.0.8`, exact project directory, empty History/tool traffic and Findings,
  proxy binding, and no
  upstream proxy before navigation and again at both mutation boundaries; managed mode also locked
  project switching, stripped inherited `INTERSEPTOR_*` settings, and disabled update checks and
  browser launches. The POSIX descriptor handoff fails closed on unsupported platforms so a
  free-port probe or replacement server can never receive full-audit mutations
- Browser: Playwright 1.60.0, with the installed Chromium, Firefox, and WebKit managed runtimes
- Data: isolated projects with generic `example.com`, `localhost`, and loopback fixtures only
- Required viewports: 1440 × 900, 1024 × 768, and 390 × 844
- Reduced motion: a separate browser context with `prefers-reduced-motion: reduce`

## Current-source applicability

The retained runtime digest, runtime file count, and harness digest are recorded under
`application_source` in [`browser-audit.json`](browser-audit.json) and checked against the current
worktree by the Go audit-evidence regression. They match the previously audited saved-workspace and
evidence-first Findings runtime exactly, including bounded startup recovery,
versioned project-local browser keys, guarded Repeater/Intruder state, and structured
Claim/Risk/Reproduction/Evidence/Fix-Retest/Review fields, screenshot and captured-flow
provenance, report readiness, safe export paths, compact-toolbar behavior, mobile navigation
semantics, and the published `2.0.8` browser-background suppression baseline.
Documentation-only commits made after the audited base do not change this identity; any later change
under `cmd/`, `internal/`, `go.mod`, or `go.sum` requires a fresh full run and replacement evidence.

The complete 132-case aggregate matrix (44 cases per engine) and three-run-per-engine performance profiles were executed against that exact source
with `python3 scripts/ui_browser_audit.py --full --managed --output-dir /tmp/interseptor-ui-custom-controls-verified`
using the default burst of 240, three performance runs, and all three browser engines. Full mode cannot accept an arbitrary
existing server: it builds and starts one owned
candidate with project switching disabled on parent-retained loopback listeners, then stops it,
closes the reservations, and removes only its validated sentinel-owned temporary root. The
validated machine-readable result and screenshots were retained
together in this directory so the pass, measurements, and application-source identity cannot drift
apart.

## Historical managed verification — 2026-09-05

This run verifies the earlier source identity recorded below; it does not certify the current runtime.

The 16 Settings captures in `supplemental/` were recaptured after navigation
transitions settled, with the selected navigation state and lack of viewport
overflow asserted. The actual custom checkbox was toggled with Space and
verified in forced-colors mode. Its screenshot and matching source identity are
recorded in [settings-stable.json](supplemental/settings-stable.json).

The exact command below completed successfully against fresh, sentinel-owned
loopback projects and retained no real traffic or engagement data:

```bash
python3 scripts/ui_browser_audit.py --full --managed \
  --output-dir /tmp/interseptor-ui-custom-controls-verified
```

- Aggregate outcome: **132/132 cases passed** — 44 each in Chromium, Firefox,
  and WebKit; there were no console, page, HTTP, or external-request failures.
- The run used the default `--burst 240` and `--perf-runs 3` workload. Current
  per-engine measurements remain in [`browser-audit.json`](browser-audit.json),
  rather than being copied into prose as a stale performance claim.
- `application_source.runtime_sha256` is
  `e9f6e196a7fbbbeb1745ac9511a9a1436ec103be7545d827a27058366469e8e2` for 253
  runtime files; `application_source.audit_harness_sha256` is
  `514e2b40c2cd79d3eed78f58cf0d2cfee06003abe1d2ef68a97b374e92209be0`.
  Both were recomputed from the worktree at the time of the run and matched the
  retained JSON.
- Supplementary Chromium managed checks covered app-rendered tooltip
  focus/Escape/hover and Map click-through, keyboard and forced-colors controls,
  hidden native select/title adapters, mobile tool navigation, and a synthetic
  Notes failed-save reason/retry path. Their 24 captures (23 primary captures and
  one forced-colors diagnostic) are retained under
  [`supplemental/`](supplemental/); they contain only disposable audit data.

Interseptor already had a strong foundation: a compact technical identity, direct navigation,
stable data hierarchy, native controls, visible raw protocol data, project-scoped state, and no
frontend runtime or external asset dependency. The release audit concentrated on state integrity
and feedback rather than changing that identity.

The highest-value defects were async ownership gaps: delayed responses could repaint a newer
selection, hide a real failure behind a plausible empty state, or overwrite edits made while a save
was pending. The audited fixes bind each acknowledgement to the object, generation, tab, editor, or
field that created it. In particular:

- A browser-specific stalled fetch can no longer leave the static “Loading saved workspace…” shell
  indefinitely. Independently settling project and hydration deadlines restore local operation,
  while an early classic-script watchdog provides one keyboard-reachable Reload action when the
  ES-module graph itself fails.
- Repeater/Intruder state rejects malformed, duplicate-ID, unsafe-ID, oversized, and over-200-tab
  envelopes before rendering. The original value remains untouched, a valid server copy can be used
  in memory, and the first explicit edit creates the safe replacement. Browser-local write failure
  no longer suppresses project-database persistence.
- Intruder preset arrays filter invalid entries and normalize every editor field before rendering,
  loading, or resaving, so a malformed browser or project value cannot abort workstation startup.
- Every Repeater creation path—including Proxy/Findings adoption, send-as identity, and Postman
  import—uses one bounded allocator. Postman capacity rejection is atomic, and project keys use a
  versioned full-identity encoding with safe migration only when an older key has one owner.
- Legacy project ownership is never inferred from deduplicated same-name projects or malformed
  project-list entries. Root/external projects both named `default`, wholly invalid lists, and mixed
  valid/malformed lists were fault-injected in every engine; name-keyed and unscoped drafts and retry
  markers stayed unchanged.
- History reconciles selection across successful and failed server-side filters and never leaves a
  stale Inspector spinner; a failed same-filter background refresh cannot cancel an independently
  selected detail request.
- Intercept removes Forward/Drop rows only after acknowledgement and keeps request/response lane
  ownership stable across SSE refreshes.
- Repeater history belongs to the tab, not to the current URL or request contents, and is removed
  only when that tab closes; IndexedDB cleanup is attempted even when its localStorage retry ledger
  is unavailable.
- Settings acknowledgements update only fields that the operator has not changed since submission.
- Session full-object saves and login runs share one mutation lane, so rapid cross-action use cannot
  restore an older form snapshot.
- Findings, Intruder, Scanner, Notes, Map, Session, and project hydration use latest-request or
  entity-scoped ownership instead of repainting newer work.
- Findings use one canonical typed envelope across UI, REST, MCP, and report export. Screenshot and
  captured-flow attachments settle pending edits first, proof metadata updates the pending snapshot
  while typing, and Done waits for both scalar and body writes before switching to the read view.
- Missing peer-flow evidence remains visibly missing through collaboration merges and later edits;
  a reused local flow id cannot silently bind a finding to unrelated traffic.
- Share no longer probes an unconfigured remote Vault, a completed Vault save cannot clear a newer
  token draft, and the flow-search Test action validates its API-required name locally.
- Captured and replayed WebSocket frames retain their distinct endpoint contracts, and selected
  records expose consistent current-state semantics to assistive technology.
- Repeater sends, cross-feature request adoption, OOB saves/refreshes, and Intruder progress each
  retain the exact action, draft, and task-tab owner that initiated them.
- Cross-feature Repeater adoption reuses only a genuinely pristine task tab; a request edited before
  or during adoption remains untouched and receives a separate incoming tab.
- Same-flow SSE refreshes cannot retarget an explicit Authz request, and Map graph summaries count
  the same filtered, collapsed, and capped endpoints that the graph actually renders.
- Finding creation, Scanner check actions, and Authz actions move pending focus to an in-dialog
  status, restore the initiating action after rejection, and keep every dismissal or scope-navigation
  exit locked until the non-cancelable request acknowledges; Authz failures use assertive alerts.
- Browser and Android suppression now share one flow policy across request rules, response rules,
  both intercept queues, History, and body capture. Current Firefox Suggest, OHTTP, DAP, Remote
  Settings, sponsored-content, connectivity, crash, and Safe Browsing hosts observed in the live
  audit are forwarded unchanged without polluting a new capture. A suppressed TLS-passthrough
  notice never claims its dedup marker, so disabling suppression restores future observability
  immediately even while an earlier suppressed CONNECT remains in flight.

## Feature and dependency matrix

| Surface | Independent checks | Cross-feature checks |
| --- | --- | --- |
| Proxy / History | filtering, selection, Inspector loading/error, pagination retry, saved-search validation, live virtualization, keyboard row/context actions | send/search actions wait for project identity; Map search receives the selected evidence; selected detail loads remain consistent during filters, failures, and SSE |
| Intercept | request/response queues, filters, Match & Replace, pending/acknowledged Forward and Drop, typing-safe shortcuts | queue refreshes cannot overwrite filter edits; operation results cannot mutate a newer selected queue item |
| Repeater | tab lifecycle, 200-tab/ID bounds, Postman import, Send states, response ownership, decode races, persistence and cleanup | every creation route shares one allocator; send-to-Repeater keeps the operator's edited tab intact; History remains tab-owned across request changes and project reload |
| Intruder | duplicate-start lock, history selection, live polling errors, result filters | returning from historical evidence to live evidence preserves the configured target; finding creation uses the displayed run |
| Scanner | real pending/success/error state, latest issues response, pending-status focus, retry focus, readable narrow layout | created findings and flow evidence remain tied to the scan result that initiated them |
| Findings | creation/focus, canonical field and body saves, Differential preset, screenshot upload, flow picker, proof annotations, provenance, report readiness, Markdown/HTML/JSON handoff, compact-toolbar geometry, evidence lightbox | screenshot and captured-flow attachments retain the active finding and source flow; Done flushes pending edits; Intruder-to-Finding and Activity/evidence focus retain their owners |
| Map | tree hydration, table/graph/search replacement, graph-summary parity, node keyboard selection, Fit/focus transform | Proxy body search waits for project hydration; host focus preserves the server-side search and refreshes parameters |
| Settings | all eight sections, search/navigation, dirty-field restoration, upstream proxy ownership, Session/project failures, device refresh, API keys, allowlist, REST/MCP, Share/Vault, explicit browser-background suppression semantics | live refresh never overwrites pending edits; external allowlist changes reconcile only the visible pane; Session Save/Login Run serialize full-object writes; project failure blocks dependent UI loads instead of guessing a project; delayed Vault acknowledgement preserves the newest token draft; suppression states that traffic remains forwarded and existing History remains intact |
| Notes / Activity | latest load/save ownership, outcome labels, filter/focus retention | panel activation and live updates preserve focused objects and their accessible outcomes |
| Auxiliary tools | Checks/Codecs modals, Decoder, project modal, OOB availability, Authz retargeting/error announcements, WebSocket capture/replay contracts | close paths restore focus; pending modal actions retain focus; busy Authz scope navigation cannot switch the underlying panel; explicit context-menu targets and later A→B→A selection changes retain the intended flow |

### Repeater history contract

The retained exact-source browser recheck sent a request from one Repeater tab, then changed its
method, URL, headers, and body. History remained attached to the tab after every edit, top-level
navigation, and a page reload; the edited request also survived the reload. A second task tab
received a distinct history. Closing the first tab removed only its IndexedDB rows. Before closing
the second, the audit forced cleanup-ledger localStorage reads to throw; IndexedDB still reached
zero, proving that the best-effort ledger cannot block tab-owned deletion. The larger 105-request
render and close-vs-send race remain covered by focused Repeater regression tests.

### Saved-workspace startup contract

The retained pass fault-injected a failed transitive module fetch, a valid module response that
throws during evaluation, and a workspace fetch that never settles or honors cancellation in
Chromium, Firefox, and WebKit. Module failures produced an assertive Reload action with dead
surfaces inert; the ignored-abort cases unlocked browser-local operation in every engine. A total project-identity failure in each engine kept unavailable
controls locked while clearing navigation and panel `aria-busy`, so the final alert no longer reads
as an indefinite load. A module delayed beyond the guard recovered the exact controls the guard
disabled when it eventually completed.

Separate contexts retained a 5,000-tab source blob while restoring a safe server tab in memory,
preserved malformed pending state, synchronized its first valid replacement edit, and proved that a
throwing `localStorage.setItem` still sends the project-backed workspace write. A 200-tab workspace
blocked toolbar, cross-feature, and Postman additions without becoming unloadable; normal Postman
import created exactly one unique tab per request. Versioned project keys distinguished `team alpha`
from `team_alpha`, migrated a uniquely owned legacy key, and left an ambiguous legacy value intact
with an explicit recovery warning. Duplicate `default` project names and wholly or partly malformed
project lists were also unable to certify ownership: Repeater, Intruder, preset, and pending legacy values
remained byte-identical while safe server state stayed usable. Malformed Repeater, Intruder, and Intruder-preset fields were
coerced or filtered without sharing Repeater history identities or blocking the editor.

When `/api/project` never settled but `/api/version` still supplied the canonical project directory,
the workstation became usable in Chromium, Firefox, and
WebKit. Each engine started with unknown-owner legacy Repeater, Intruder, and preset values, then
fault-injected the first project PUT for all three surfaces. New edits remained in both their exact
canonical-directory browser keys and pending-retry keys, survived a reload, retried successfully,
and cleared the pending markers. Every older unscoped and name-keyed value—including all three
legacy pending queues—remained byte-for-byte unchanged; only its migration was deferred.

### Findings evidence contract

The retained exact-source browser pass created a generic finding, saved the structured claim/risk/
target/fix/retest/confidence envelope, applied the optional Differential outline, pasted a valid
PNG while the final reproduction textarea still owned focus, and selected a captured loopback flow
by clicking its picker row text. The pass confirmed the in-progress step survived the authoritative
image response and the flow selection toggled exactly once. It
then wrote a proof statement for each artifact, left Edit through the normal Done/flush path, and
observed canonical `report_ready` readiness with `operator_upload` and `captured_flow` /
`sourceFlowId` provenance still visible. Markdown, self-contained HTML, and JSON handoffs each
completed through the UI download path.

The final Findings views are retained as
[`findings-after-1440x900.png`](findings-after-1440x900.png),
[`findings-after-1024x768.png`](findings-after-1024x768.png), and
[`findings-after-390x844.png`](findings-after-390x844.png). The 1024 px geometry assertion verifies
that Export and Group by tag do not intersect and that the title keeps a full readable row; the
390 px pass verifies the horizontal main navigation, vertical Findings listbox, direct detail route,
Back behavior, and focus restoration. These three Findings captures and the required Proxy/Map/
Scanner captures were regenerated by the same managed exact-source run and are dimension-checked by
the retained-audit test.

### Settings ownership contract

The retained exact-source browser pass opened every Settings section, refreshed both device panels,
exercised the project modal and reversible API-key/allowlist mutations, and verified that an
unconfigured Vault made no remote request. A direct API mutation then simulated another client:
the visible Allowlist pane reconciled both its addition and deletion through `allowlist.update`.
The pass also held a Vault configuration PUT, typed a newer token draft, then released the
acknowledgement; the newer draft remained. Focused Go UI contracts additionally inject delayed
Settings reads and acknowledgements, rapid strict/compatible changes, live refresh over dirty TLS
drafts, and shared Setup/Settings system-proxy mutations without making an operating-system change.

The exact-source Playwright pass exercised the browser-background toggle using only the keyboard.
Both server-acknowledged states exposed the expected `aria-pressed` value and plain-language label,
the control restored visible focus after its temporary pending lock, and suppression was restored
before teardown. The Settings surface had `0 px` document overflow at both 1440 × 900 and 390 × 844,
and the pass observed no console errors, page errors, or external requests.

### OOB draft ownership contract

The retained exact-source browser pass temporarily enabled OOB only in its isolated project, opened the
local interaction modal, entered and blurred a newer base-URL draft, then cleared interactions. The
authoritative interaction refresh did not overwrite the draft. The pass closed the modal and
restored OOB disabled; it made no external callback or system-proxy change.

## Browser and accessibility checks

- All ten top-level tabs committed `aria-selected`, active-panel state, and focus together.
- Every primary panel and its key control remained reachable at 390 × 844; 1440 × 900,
  1024 × 768, and 390 × 844 had `0 px` document overflow.
- The main navigation announces vertical orientation on the desktop rail and horizontal orientation
  on the narrow scrolling strip; the Findings listbox remains vertical at both sizes.
- Reduced motion produced `0` active animations, `0s` panel/row transitions, and automatic rather
  than smooth scrolling. The shared JavaScript helper also returned without creating an Animation;
  selection, text, color, border, and status still communicated the result.
- Startup recovery preserved alert/status semantics and keyboard access in Chromium, Firefox, and
  WebKit; slow-module recovery restored only watchdog-owned inert/disabled state.
- Map retained one roving graph tab stop, keyboard movement, `aria-selected`, and a persistent
  selection halo; Fit plus wheel/drag changed the graph transform without a continuous simulation.
- Activity kept focus on the same record during live insertion and announced success/error outcome
  text. The evidence lightbox opened from the keyboard and restored focus on Escape.
- Real Scanner and Intruder runs reached pending and success states. Rejected Finding, Scanner-check,
  and Authz mutations retained in-modal focus, announced the failure, restored their initiating
  action for retry, and kept Escape/backdrop/scope exits locked until acknowledgement. Intercept Forward and response
  Drop kept their queue items until deliberately delayed server acknowledgements completed. Authz
  retained its explicit context target and followed later A→B→A selection changes exactly.
- Every engine's normal sweep had no unexpected console, page, or HTTP errors and no external
  browser request. Fault contexts intentionally returned exact `409`/`503` responses across
  recovery and ownership paths; their expected HTTP and network-console messages were classified
  separately per engine.

## Performance findings

Each engine's full profile was captured after a fresh launch of the exact runtime source identified in
`browser-audit.json`. Performance sampling used the Performance Observer API, three independent
240-request capture runs per engine, bounded-DOM assertions, and real Map Fit/wheel/drag input;
Chromium additionally retained Chrome DevTools Protocol task, script, and layout counters. No
browser long task or stuck busy control was observed.

| Engine | Retained evidence |
| --- | --- |
| Chromium | `metrics.by_engine.chromium`: network and interaction p95, bounded History rows/nodes, long tasks, Map readiness/gesture latency, and CDP task/script/layout deltas |
| Firefox | `metrics.by_engine.firefox`: network and interaction p95, bounded History rows/nodes, long tasks, and Map readiness/gesture latency |
| WebKit | `metrics.by_engine.webkit`: network and interaction p95, bounded History rows/nodes, long tasks, and Map readiness/gesture latency |

The retained exact-source recheck treats only a visible busy surface as busy. The virtualized table remained
interactive after each engine's 720 requests, preserved its scroll state through Map navigation,
and left no visible busy surface behind.

## Competitive review

The audit compared Interseptor's workflows with official documentation for
[Burp Suite tools](https://portswigger.net/burp/documentation/desktop/tools) and
[Organizer](https://portswigger.net/burp/documentation/desktop/tools/organizer),
[OWASP ZAP Sites](https://www.zaproxy.org/docs/desktop/ui/tabs/sites/),
[Caido Replay](https://docs.caido.io/app/quickstart/replay),
[Caido interception](https://docs.caido.io/app/guides/intercept_traffic) and
[shortcuts](https://docs.caido.io/app/reference/command_shortcuts), and
[mitmproxy](https://docs.mitmproxy.org/stable/).

The useful common pattern is a dense primary evidence surface with stable per-task ownership,
keyboard access, and direct transitions into replay/topology/reporting tools. That supports the
tab-owned Repeater history, retained History/Map tables, explicit queue acknowledgement, and direct
shortcuts implemented here. It does not justify adopting a generic dashboard, decorative motion,
or a component-library visual language.

## Intentionally deferred

- An optional 3D topology remains unjustified until it demonstrates better host/cluster/path
  comprehension than the accessible table, tree, and SVG graph. See `docs/ui-motion-spec.md`.
- A richer query language, workflow engine, advanced breakpoint rules, and forced-browsing features
  need separate product/API design and evidence of demand; they were not added during a UI integrity
  pass.
- Native assistive-technology testing is still a useful release follow-up. This pass covered browser
  semantics, keyboard behavior, focus ownership, reduced motion, and accessible status text.

## Required source gates

Current local results: build, `go vet ./...`, documentation checks, JavaScript
syntax checks, and the control package (including its race run) passed.
`go test ./...` and `go test -race ./...` each failed only the pre-existing
`TestRunUpdateCheckOnly`: its live release lookup reports `release v2.0.8 not found`.
The same failure was observed before this UI change; no race warnings were reported.

Any branch that changes the runtime must pass these source gates in its owning validation phases:

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
CGO_ENABLED=0 go build ./cmd/interseptor
go run ./tools/docscheck check .
for file in internal/control/ui/js/*.js; do node --check "$file"; done
```

The branch is additionally required to pass the no-mistakes review and repository CI before merge.

## Boot smoke test (run before releasing UI changes)

`scripts/ui_boot_smoke.mjs` is a fast, dependency-free check that the embedded UI still boots.
It drives a headless Chrome over the DevTools protocol (Node 22+, global `fetch`/`WebSocket`),
loads the given URL, records uncaught exceptions, console errors and HTTP responses of 400 or
more, clicks every `[data-tab]`, and exits non-zero on any uncaught exception or when
`#workspaceHydrationStatus` carries `data-workspace-boot-failed` ("Workspace scripts could not
start in this browser"). It catches boot-time `ReferenceError`s that the Go and `node --test`
suites cannot see, such as the one that shipped in v2.4.1 and was fixed in v2.4.2.

```bash
CGO_ENABLED=0 go build -o /tmp/isp ./cmd/interseptor
/tmp/isp --control-port 19966 --proxy-port 18080 --data-dir "$(mktemp -d)" &
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
  --remote-debugging-port=9333 --user-data-dir="$(mktemp -d)" --no-first-run about:blank &
node scripts/ui_boot_smoke.mjs http://127.0.0.1:19966/ --cdp http://127.0.0.1:9333
```

Always use a throwaway `--data-dir`. The script needs Chrome, so it is not part of CI.
