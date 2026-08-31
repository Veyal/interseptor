# UI release verification — 2026-08-31

## Revision and environment

- Application source: `42461e18fd13e72047cb81d99716fa6f18e8241a`
- Launch: fresh `go run ./cmd/interseptor` build of that checkout
- Browser: Playwright 1.55.0, Chromium 140.0.7339.16, with Chrome DevTools Protocol metrics
- Data: isolated projects with generic `example.com`, `localhost`, and loopback fixtures only
- Required viewports: 1440 × 900, 1024 × 768, and 390 × 844
- Reduced motion: a separate browser context with `prefers-reduced-motion: reduce`

The screenshots are committed separately from the source so this document can name the exact UI
revision it validates.

## Audit outcome

Interseptor already had a strong foundation: a compact technical identity, direct navigation,
stable data hierarchy, native controls, visible raw protocol data, project-scoped state, and no
frontend runtime or external asset dependency. The release audit concentrated on state integrity
and feedback rather than changing that identity.

The highest-value defects were async ownership gaps: delayed responses could repaint a newer
selection, hide a real failure behind a plausible empty state, or overwrite edits made while a save
was pending. The final fixes bind each acknowledgement to the object, generation, tab, or field that
created it. In particular:

- History reconciles selection across successful and failed server-side filters and never leaves a
  stale Inspector spinner.
- Intercept removes Forward/Drop rows only after acknowledgement and keeps request/response lane
  ownership stable across SSE refreshes.
- Repeater history belongs to the tab, not to the current URL or request contents, and is removed
  only when that tab closes.
- Settings acknowledgements update only fields that the operator has not changed since submission.
- Findings, Intruder, Scanner, Notes, Map, Session, and project hydration use latest-request or
  entity-scoped ownership instead of repainting newer work.

## Feature and dependency matrix

| Surface | Independent checks | Cross-feature checks |
| --- | --- | --- |
| Proxy / History | filtering, selection, Inspector loading/error, pagination retry, live virtualization, keyboard row/context actions | send/search actions wait for project identity; Map search receives the selected evidence; selected flows remain consistent during filters and SSE |
| Intercept | request/response queues, filters, Match & Replace, pending/acknowledged Forward and Drop, typing-safe shortcuts | queue refreshes cannot overwrite filter edits; operation results cannot mutate a newer selected queue item |
| Repeater | tab lifecycle, Send states, response ownership, decode races, persistence and cleanup | send-to-Repeater keeps the operator's edited tab intact; History remains tab-owned across request changes and project reload |
| Intruder | duplicate-start lock, history selection, live polling errors, result filters | returning from historical evidence to live evidence preserves the configured target; finding creation uses the displayed run |
| Scanner | real pending/success/error state, latest issues response, readable narrow layout | created findings and flow evidence remain tied to the scan result that initiated them |
| Findings | creation focus, save ordering, rollback, picker query ownership, evidence lightbox | flow picker and Intruder-to-Finding actions retain the active finding/run; Activity and evidence focus are restored |
| Map | tree hydration, table/graph/search replacement, node keyboard selection, Fit/focus transform | Proxy body search waits for project hydration; host focus preserves the server-side search and refreshes parameters |
| Settings | search/navigation, dirty-field restoration, upstream proxy ownership, Session/project failures, device guidance | live refresh never overwrites pending edits; project failure blocks dependent UI loads instead of guessing a project |
| Notes / Activity | latest load/save ownership, outcome labels, filter/focus retention | panel activation and live updates preserve focused objects and their accessible outcomes |

### Repeater history contract

The exact-target browser recheck sent a request from one Repeater tab, then changed its method, URL,
headers, and body. History remained attached to the tab after every edit and a page reload; the
edited request also survived the reload. The durable IndexedDB row count stayed at one until the
tab closed, then reached zero. The larger 105-request render and close-vs-send race remain covered
by the focused Repeater regression tests.

## Browser and accessibility checks

- All ten top-level tabs committed `aria-selected`, active-panel state, and focus together.
- 1440 × 900, 1024 × 768, and 390 × 844 had `0 px` document overflow.
- Reduced motion produced `0` active animations, `0s` panel/row transitions, and automatic rather
  than smooth scrolling; selection, text, color, border, and status still communicated the result.
- Map retained one roving graph tab stop, keyboard movement, `aria-selected`, and a persistent
  selection halo.
- Activity kept focus on the same record during live insertion and announced success/error outcome
  text. The evidence lightbox opened from the keyboard and restored focus on Escape.
- Normal sweeps had no unexpected console errors, page errors, or external hosts. Fault-injection
  runs intentionally aborted requests or returned `500`/`503`; each produced a persistent Retry or
  rollback state and those expected network-console messages were classified separately.

## Performance findings

The missing exact-target profile was repeated after a fresh launch of source `42461e1`.

| Scenario | Result |
| --- | --- |
| 240-request live History burst | `505.2 ms` network wall time, 101 observed rows at 1440 × 900, 3,087 DOM nodes, 0 long tasks, no visible stuck busy state |
| Map hydration after the burst | `970.1 ms` from tab activation to the first rendered host |
| Whole 240-request profile (CDP delta) | task `0.115 s`, script `0.057 s`, layout `0.015 s`, heap `-0.6 MB` after collection |

The target recheck treats only a visible busy surface as busy. The virtualized table remained
interactive after the burst and no visible busy surface remained.

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
