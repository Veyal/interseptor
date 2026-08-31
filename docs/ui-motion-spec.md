# UI motion specification

Interseptor uses motion to explain state changes in a dense security workspace. Motion is never decoration: it must identify what changed, connect an action to its result, show live processing, preserve selection, or clarify a data relationship. The table, raw protocol data, status text, and keyboard model remain primary.

## Principles

- **State before spectacle.** Color, text, borders, icons, and ARIA state carry the meaning; motion reinforces it.
- **Local before global.** Animate the changed panel, row, control, response, node, or edge rather than the application shell or a large subtree.
- **Once before continuous.** New-data signals run once. Infinite animation is reserved for capture, reconnecting, and enabled live indicators.
- **Stable geometry.** Dense rows do not translate or scale on hover. Response updates do not resize the surrounding workspace.
- **Truthful timing.** Exit motion follows a successful acknowledgement. Pending states reflect a real request. No simulated progress is shown.
- **Owned completion.** A delayed response, timer, file picker, or focus callback belongs to the exact tab, object, editor generation, and action that started it. If that context changes, the completion is discarded without repainting newer work.

## Tokens

| Token | Value | Use |
| --- | --- | --- |
| `--motion-instant` | `80ms` | pressed and immediate acknowledgement states |
| `--motion-fast` | `120ms` | borders, focus, and compact control states |
| `--motion-base` | `180ms` | panel and content transitions |
| `--motion-slow` | `260ms` | one-shot data arrival and graph pan/zoom |
| `--motion-live` | `1040ms` | approved reconnect/capture live cycle |
| `--motion-live-slow` | `1300ms` | approved enabled-control live cycle |
| `--motion-standard` | `cubic-bezier(.4, 0, .2, 1)` | reversible state changes |
| `--motion-exit` | `cubic-bezier(.4, 0, 1, 1)` | removal after acknowledgement |
| `--motion-enter` | `cubic-bezier(.2, .8, .2, 1)` | newly available content |

CSS uses these tokens directly. One-shot JavaScript motion uses the matching subset of values from `js/motion.js`, calls `Element.animate()`, cancels an older animation on the same element, and is skipped while the document is hidden.

## Component behavior

### Navigation

Main-tab state, ARIA selection, focus, and panel visibility commit synchronously. The newly active panel then receives one 180ms Web Animations API entrance: opacity plus no more than 6px of movement in the navigation direction. A deferred View Transition update is deliberately rejected for the tab surface because it can leave keyboard focus on a tab whose panel is not yet active. Unsupported browsers switch immediately and preserve panel scroll position and local state.

### Proxy history

A newly inserted request may receive a finite accent-edge/background signal. The identity is consumed after its first DOM insertion, so virtualization and later row patches do not replay it. Signals are suppressed once a short update window becomes a burst; high-volume capture remains a stable table.

Changing the selected flow clears the previous detail before the new request begins. Loading and failure are persistent text states with actions disabled, so the selected row, visible protocol data, and available cross-tool actions always refer to the same flow. A known client-decidable filter mismatch or deletion closes the Inspector without an exit animation. When server-side search or scope matching cannot prove whether an older paged selection still belongs, the Inspector pauses on an explicit unavailable state instead of displaying unconfirmed data; clearing the filter can recover the selection. Failed History refreshes leave a direct Retry state rather than an indefinite loading indicator. Pagination and scope failures stay inline and retryable; failure feedback does not travel or pulse.

### Intercept

A newly held request or response receives one finite queue-arrival signal, capped during bursts. Forward and Drop remain pending while their real API requests are active. If an SSE removal arrives first, the row is retained briefly so its exit starts only after acknowledgement. Forward fades in the continuation direction; Drop uses a short neutral removal fade. Selection is keyed by request ID and side and is retained when possible.

### Repeater and Scanner

The action control exposes idle, pending, success, and error state through text, color, `aria-busy`, and disabled state. Response content receives a local opacity transition after real data or an error arrives. Raw status and error information remain visible. Scanner progress continues to use backend-reported progress only.

Repeater send history belongs to the task tab rather than the current URL or request fields. Method, URL, header, body, response selection, top-level navigation, and reload changes do not move or clear it; closing the task tab removes its durable history. Cross-tool loads and Intruder file pickers validate their originating tab and editor generation before updating controls.

### Map

Tree, table, parameter, and SVG graph views remain available. The SVG graph compares stable node/edge identities with the preceding render and signals only new or changed items, with a cap that suppresses bulk redraw animation. New edges use a short directional dash signal. Nodes are keyboard-operable, expose selection state, and show a focus/selection halo. Fit and host-focus pan/zoom are finite user-initiated transforms; initial layout and bulk filtering do not animate every node.

### Existing live states

Toast entry/exit, control state, capture-live, reconnecting, and enabled-live indicators use shared tokens. Pending response text is static rather than an infinite blink so heavy capture cannot create an animation per row.

At phone widths the control-stream dot remains visible, and its text expands only for the important reconnecting state. Live Settings refreshes preserve dirty fields and dynamic rows without animating the form or moving focus. Activity refreshes, context menus, and evidence lightboxes preserve or deliberately restore focus; motion never substitutes for their accessible label, outcome, or selection state.

Security context and reference data never fail into a plausible empty state. Scope, identities, projects, saved views/tags, rule packs, sharing, and device/API references keep an inline error and direct Retry action; recovery replaces that state without moving the operator to another panel.

## Reduced motion

`prefers-reduced-motion: reduce` disables non-essential CSS animation, transitions, and smooth scrolling. JavaScript helpers also return immediately. Every state remains understandable through persistent text, color, border, icon, selection, and final position. Focus is never moved to facilitate animation.

## Performance rules

- Prefer opacity and transform; do not animate layout dimensions except real progress width.
- Do not create per-row animation objects during traffic bursts.
- Consume one-shot row/node identities and cancel superseded element animations.
- Do not animate large DOM subtrees, every SVG item after a filter, or a continuous graph simulation.
- Skip non-essential motion while the document is hidden.
- Cancel or ignore delayed callbacks whose initiating action, tab, selection, or editor generation is no longer current.
- Keep map readability caps and history virtualization in force.
- Add no animation runtime, external request, font, or hosted asset.

## Allowed and rejected examples

Allowed:

- a one-time 260ms accent edge on one newly captured request;
- a Send control that reads `Sending…`, then shows a finite success or error state;
- a selected graph node with a persistent halo and a one-time new-edge dash;
- an acknowledged intercept item fading a few pixels toward its outcome.
- a persistent loading/error message replacing stale Inspector data while the correct flow is fetched.

Rejected:

- translating/scaling table rows on hover;
- replaying row entry motion after virtualization or sorting;
- animating every node after filtering or every request during a burst;
- fake progress, bouncing controls, animated backgrounds, particles, glow effects, or `transition: all`;
- motion as the only signal of pending, success, failure, selection, or connection state.
- keeping old request data visible under a newly selected row merely to avoid an abrupt content change.

## Optional 3D topology assessment

3D is intentionally deferred. It may be useful only if a future prototype proves that depth improves comprehension of host relationships, endpoint clusters, active paths, finding clusters, or time-based traffic behavior beyond the current table/tree/SVG combination. Evaluation should use real dense captures and compare task completion, occlusion, keyboard navigation, selection persistence, and rendering cost. A 3D view would remain optional; the table and SVG graph would stay the primary, accessible interfaces. Decorative 3D or a continuously simulated topology is not justified.
