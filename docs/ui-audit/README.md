# UI audit screenshots

These screenshots document the UI audit completed at `b5dac28`. They compare the repository baseline
(`ec1b79e`) with that audited revision in isolated, empty projects and contain no captured request
data. They do not validate later UI revisions.

| Viewport | Surface | Before | After |
| --- | --- | --- | --- |
| 1440 × 900 | Proxy / History | [`before-1440x900-proxy.png`](before-1440x900-proxy.png) | [`after-1440x900-proxy.png`](after-1440x900-proxy.png) |
| 1024 × 768 | Map | [`before-1024x768-map.png`](before-1024x768-map.png) | [`after-1024x768-map.png`](after-1024x768-map.png) |
| 390 × 844 | Scanner | [`before-390x844-scanner.png`](before-390x844-scanner.png) | [`after-390x844-scanner.png`](after-390x844-scanner.png) |

The narrow Scanner pair shows the most visible geometry correction: the issue list and detail pane now stack instead of compressing the detail to an unreadable rail. History retains its intentionally horizontal, scrollable dense-table surface while its controls wrap within the viewport.

## Browser and performance evidence

Playwright exercised every top-level panel at all three viewports. The final sweep reported no unexpected console errors, page errors, failed requests, duplicate IDs, nested interactive controls, unnamed visible controls, or document-level horizontal overflow. A reduced-motion context committed the same selected tab and focus state with zero panel animations.

Chrome DevTools Protocol `Performance` metrics were collected from headless Chromium for the required state changes:

| Scenario | Renderer task | Script | Layout | Long tasks |
| --- | ---: | ---: | ---: | ---: |
| 180 ms panel transition | 36.863 ms | 1.508 ms | 1.881 ms | 0 |
| 240-request live History burst | 81.860 ms total | 29.597 ms | 13.733 ms | 0 |
| acknowledged Intercept queue update | 12.138 ms | 0.926 ms | 0.538 ms | 0 |
| 110-node Map render + Fit + zoom | 29.664 ms | 4.788 ms | 4.787 ms | 0 |

The live History burst retained a bounded 101-row virtualized DOM window. Separate functional runs captured 360 requests with no long tasks, verified Repeater single-flight behavior, ran the passive Scanner, rendered and keyboard-navigated the Map graph, exercised Finding failure rollback, and forwarded/dropped held requests. Delayed-response tests also confirmed latest-request-wins History/Map/Findings behavior, bounded project-state hydration, and project-scoped tab restoration.
