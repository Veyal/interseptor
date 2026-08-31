# UI audit screenshots

The **After** screenshots validate UI source revision
[`42461e1`](https://github.com/Veyal/interseptor/commit/42461e18fd13e72047cb81d99716fa6f18e8241a).
They were captured from a fresh build of that checkout in the isolated
`evidence-target-42461e1` project on 2026-08-31. The evidence change contains only documentation,
screenshots, and the changelog, so `42461e1` remains the exact application source under test.

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

The final Playwright recheck ran Chromium 140 against source `42461e1`. The 1440 × 900 Proxy,
1024 × 768 Map, and 390 × 844 Scanner views each had zero document-level horizontal overflow.
Reduced motion left zero active animations and zero-duration animations/transitions. Repeater
history stayed tab-owned after method, URL, header, and body edits and a reload, then its IndexedDB
row was removed when the tab closed. No unexpected console or page errors were reported.

Chrome DevTools Protocol metrics covered a fresh 240-request proxy burst and the resulting Map
hydration. The 1440 × 900 History window stayed bounded at 101 rendered rows and about 3.1k DOM
nodes, with no long tasks or visible stuck busy state.

See [`verification.md`](verification.md) for the revision, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
