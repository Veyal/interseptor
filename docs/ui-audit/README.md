# UI audit screenshots

The **After** screenshots validate UI source revision
[`3ac5891`](https://github.com/Veyal/interseptor/commit/3ac5891c25d11f4e38499b0f3629b243ec6260ad).
They were captured from a fresh build of that checkout in the isolated
`ui-independent-3ac5891` project on 2026-08-31. The screenshots and measurements remain scoped to
that checkout; later application commits on the release branch are outside this evidence.

The retained **Before** screenshots are from repository baseline `ec1b79e`. The After set uses only
the audit's local loopback fixture and generic project records; neither set contains real request
data, personal data, or target information.

| Viewport | Surface | Before | After |
| --- | --- | --- | --- |
| 1440 × 900 | Proxy / History | [`before-1440x900-proxy.png`](before-1440x900-proxy.png) | [`after-1440x900-proxy.png`](after-1440x900-proxy.png) |
| 1024 × 768 | Map | [`before-1024x768-map.png`](before-1024x768-map.png) | [`after-1024x768-map.png`](after-1024x768-map.png) |
| 390 × 844 | Scanner | [`before-390x844-scanner.png`](before-390x844-scanner.png) | [`after-390x844-scanner.png`](after-390x844-scanner.png) |

The narrow Scanner pair shows the most visible geometry correction: the issue list and detail pane
stack instead of compressing the detail to an unreadable rail. History keeps its intentionally
horizontal, scrollable dense-table surface while its controls wrap inside the viewport.

## Verification summary

The audited-source Playwright recheck ran Chromium 148 against source `3ac5891`. All 17 independent
and cross-feature cases passed at 1440 × 900, 1024 × 768, and 390 × 844 without document overflow.
Reduced motion left zero active animations and zero-duration animations/transitions. Repeater
history stayed tab-owned after every request edit, navigation, reload, a second task tab, and both
tab closures, including when its localStorage cleanup ledger was forced unavailable. The audit also
exercised OOB draft retention across interaction refresh, real Scanner and Intruder runs, delayed
Intercept acknowledgements, Authz retargeting, reversible API-key/allowlist actions, unconfigured Vault
behavior, and every Settings section. No unexpected console, page, HTTP, or external-request errors
were reported.

Chrome DevTools Protocol metrics covered three fresh 240-request proxy bursts and the resulting Map
hydration and gestures. The 1440 × 900 History window stayed bounded at 82 rendered rows and 4,825
DOM nodes, with no long tasks or visible stuck busy state.

See [`verification.md`](verification.md) for the revision, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
