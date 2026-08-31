# UI release verification — 2026-08-31

## Revision and environment

- Final application source: `0db243da77f26a9fb4a4a2598af622d17d4faa4d`
- Original audit baseline source: `42461e18fd13e72047cb81d99716fa6f18e8241a`
- Launch: fresh `go run ./cmd/interseptor` build of the exact final source
- Browser: Playwright 1.60.0, Chromium 148.0.7778.96
- Data: isolated projects with generic `example.com`, `localhost`, and loopback fixtures only
- Required viewports: 1440 × 900, 1024 × 768, and 390 × 844
- Reduced motion: a separate browser context with `prefers-reduced-motion: reduce`

The complete 17-case matrix and three-run performance profile were executed against the exact final
source with `scripts/ui_browser_audit.py --full --burst 240 --perf-runs 3`. Two preceding isolated
passes and one independent-agent pass also completed the same matrix without a failure. The
screenshots are committed separately so this document can name the exact application revision they
validate.

## Audit outcome

Interseptor already had a strong foundation: a compact technical identity, direct navigation,
stable data hierarchy, native controls, visible raw protocol data, project-scoped state, and no
frontend runtime or external asset dependency. The release audit concentrated on state integrity
and feedback rather than changing that identity.

The highest-value defects were async ownership gaps: delayed responses could repaint a newer
selection, hide a real failure behind a plausible empty state, or overwrite edits made while a save
was pending. The final fixes bind each acknowledgement to the object, generation, tab, editor, or
field that created it. In particular:

- History reconciles selection across successful and failed server-side filters and never leaves a
  stale Inspector spinner.
- Intercept removes Forward/Drop rows only after acknowledgement and keeps request/response lane
  ownership stable across SSE refreshes.
- Repeater history belongs to the tab, not to the current URL or request contents, and is removed
  only when that tab closes.
- Settings acknowledgements update only fields that the operator has not changed since submission.
- Findings, Intruder, Scanner, Notes, Map, Session, and project hydration use latest-request or
  entity-scoped ownership instead of repainting newer work.
- Share no longer probes an unconfigured remote Vault, a completed Vault save cannot clear a newer
  token draft, and the flow-search Test action validates its API-required name locally.
- Captured and replayed WebSocket frames retain their distinct endpoint contracts, and selected
  records expose consistent current-state semantics to assistive technology.

## Feature and dependency matrix

| Surface | Independent checks | Cross-feature checks |
| --- | --- | --- |
| Proxy / History | filtering, selection, Inspector loading/error, pagination retry, saved-search validation, live virtualization, keyboard row/context actions | send/search actions wait for project identity; Map search receives the selected evidence; selected flows remain consistent during filters and SSE |
| Intercept | request/response queues, filters, Match & Replace, pending/acknowledged Forward and Drop, typing-safe shortcuts | queue refreshes cannot overwrite filter edits; operation results cannot mutate a newer selected queue item |
| Repeater | tab lifecycle, Send states, response ownership, decode races, persistence and cleanup | send-to-Repeater keeps the operator's edited tab intact; History remains tab-owned across request changes and project reload |
| Intruder | duplicate-start lock, history selection, live polling errors, result filters | returning from historical evidence to live evidence preserves the configured target; finding creation uses the displayed run |
| Scanner | real pending/success/error state, latest issues response, readable narrow layout | created findings and flow evidence remain tied to the scan result that initiated them |
| Findings | creation focus, save ordering, rollback, picker query ownership, evidence lightbox | flow picker and Intruder-to-Finding actions retain the active finding/run; Activity and evidence focus are restored |
| Map | tree hydration, table/graph/search replacement, node keyboard selection, Fit/focus transform | Proxy body search waits for project hydration; host focus preserves the server-side search and refreshes parameters |
| Settings | all eight sections, search/navigation, dirty-field restoration, upstream proxy ownership, Session/project failures, device refresh, API keys, allowlist, REST/MCP, Share/Vault | live refresh never overwrites pending edits; project failure blocks dependent UI loads instead of guessing a project; delayed Vault acknowledgement preserves the newest token draft |
| Notes / Activity | latest load/save ownership, outcome labels, filter/focus retention | panel activation and live updates preserve focused objects and their accessible outcomes |
| Auxiliary tools | Checks/Codecs modals, Decoder, project modal, OOB availability, Authz retargeting, WebSocket capture/replay contracts | close paths restore focus; explicit context-menu targets and later A→B→A selection changes retain the intended flow |

### Repeater history contract

The exact-target browser recheck sent a request from one Repeater tab, then changed its method, URL,
headers, and body. History remained attached to the tab after every edit, top-level navigation, and
a page reload; the edited request also survived the reload. A second task tab received a distinct
history. Closing the first tab removed only its IndexedDB rows, and closing the second removed its
remaining rows. The larger 105-request render and close-vs-send race remain covered by focused
Repeater regression tests.

### Settings ownership contract

The exact-source browser pass opened every Settings section, refreshed both device panels, exercised
the project modal and reversible API-key/allowlist mutations, and verified that an unconfigured
Vault made no remote request. It held a Vault configuration PUT, typed a newer token draft, then
released the acknowledgement; the newer draft remained. Focused Go UI contracts additionally
inject delayed Settings reads and acknowledgements, rapid strict/compatible changes, live refresh
over dirty TLS drafts, and shared Setup/Settings system-proxy mutations without making an operating-
system change.

## Browser and accessibility checks

- All ten top-level tabs committed `aria-selected`, active-panel state, and focus together.
- Every primary panel and its key control remained reachable at 390 × 844; 1440 × 900,
  1024 × 768, and 390 × 844 had `0 px` document overflow.
- Reduced motion produced `0` active animations, `0s` panel/row transitions, and automatic rather
  than smooth scrolling. The shared JavaScript helper also returned without creating an Animation;
  selection, text, color, border, and status still communicated the result.
- Map retained one roving graph tab stop, keyboard movement, `aria-selected`, and a persistent
  selection halo; Fit plus wheel/drag changed the graph transform without a continuous simulation.
- Activity kept focus on the same record during live insertion and announced success/error outcome
  text. The evidence lightbox opened from the keyboard and restored focus on Escape.
- Real Scanner and Intruder runs reached pending and success states. Intercept Forward and response
  Drop kept their queue items until deliberately delayed server acknowledgements completed. Authz
  retained its explicit context target and followed later A→B→A selection changes exactly.
- The normal sweep had no unexpected console, page, or HTTP errors and no external browser request.
  The fault context intentionally returned three `503` responses for Activity/History and produced
  persistent Retry states; those expected network-console messages were classified separately.

## Performance findings

The full profile was captured after a fresh launch of exact source `0db243d`. Performance sampling
used Chrome DevTools Protocol metrics, the Long Tasks API, three independent 240-request capture
runs, bounded-DOM assertions, and real Map Fit/wheel/drag input. No browser long task or stuck busy
control was observed.

| Scenario | Result |
| --- | --- |
| Three 240-request live History bursts | network p95 `164.2 ms`; 82 rendered rows at 1440 × 900; 4,825 DOM nodes; long-task p95 `0 ms` |
| Main-panel transitions | CSS duration `180 ms`; measured completion p95 `196.8 ms` across nine panel changes |
| Delayed Intercept acknowledgement | `319.3 ms` verification window, including the deliberate route hold; row remained present until acknowledgement |
| Map after the burst | first host `75.9 ms`; Fit plus wheel/drag verification window `443.8 ms`; graph transform changed |
| Whole three-burst profile (CDP delta) | task `0.407 s`, script `0.109 s`, layout `0.044 s` |
| Map interaction (CDP delta) | task `0.046 s`, script `0.0017 s`, layout `0.0005 s` |

The target recheck treats only a visible busy surface as busy. The virtualized table remained
interactive after all 720 requests, preserved its scroll state through Map navigation, and left no
visible busy surface behind.

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

The release branch must pass these source gates in their owning validation phases:

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
CGO_ENABLED=0 go build ./cmd/interseptor
go run ./tools/docscheck check
for file in internal/control/ui/js/*.js; do node --check "$file"; done
```

The release branch is additionally required to pass the no-mistakes review and repository CI before
merge and tagging.
