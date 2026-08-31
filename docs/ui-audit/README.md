# UI audit screenshots

The **After** screenshots validate UI source revision
[`46eccdf`](https://github.com/Veyal/interseptor/commit/46eccdfac320eb2348d11f2394b19e56c6ff9b09).
They were captured from a fresh `CGO_ENABLED=0` static binary in the isolated
`evidence-final` project on 2026-08-31. The evidence commit that contains the images changes only
documentation and screenshots, so `46eccdf` remains the exact application source under test.

The retained **Before** screenshots are from repository baseline `ec1b79e`. Both sets use empty,
generic projects; they contain no captured request data or target information.

| Viewport | Surface | Before | After |
| --- | --- | --- | --- |
| 1440 × 900 | Proxy / History | [`before-1440x900-proxy.png`](before-1440x900-proxy.png) | [`after-1440x900-proxy.png`](after-1440x900-proxy.png) |
| 1024 × 768 | Map | [`before-1024x768-map.png`](before-1024x768-map.png) | [`after-1024x768-map.png`](after-1024x768-map.png) |
| 390 × 844 | Scanner | [`before-390x844-scanner.png`](before-390x844-scanner.png) | [`after-390x844-scanner.png`](after-390x844-scanner.png) |

The narrow Scanner pair shows the most visible geometry correction: the issue list and detail pane
stack instead of compressing the detail to an unreadable rail. History keeps its intentionally
horizontal, scrollable dense-table surface while its controls wrap inside the viewport.

## Verification summary

Playwright exercised every top-level panel, cross-feature state ownership, keyboard/focus behavior,
the three required viewport sizes, failure/retry states, reduced motion, Repeater tab history, and
live traffic bursts against the embedded UI. Normal runs reported no unexpected console or page
errors, no external asset hosts, and no document-level horizontal overflow.

Chrome DevTools Protocol performance measurements covered the 180 ms panel transition, repeated
240- and 360-request History bursts, acknowledged Intercept queue removal, and Map render,
pan/zoom, and Fit. The tested 1440 × 900 burst window stayed at approximately 100 rendered rows,
with a 16.8 ms maximum sampled frame gap and no long tasks.

See [`verification.md`](verification.md) for the revision, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
