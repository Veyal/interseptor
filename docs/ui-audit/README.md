# UI audit screenshots

The **After** screenshots validate UI source revision
[`0db243d`](https://github.com/Veyal/interseptor/commit/0db243da77f26a9fb4a4a2598af622d17d4faa4d).
They were captured from a fresh build of that checkout in the isolated
`ui-exact` project on 2026-08-31. The evidence change contains only documentation,
screenshots, and the changelog, so `0db243d` remains the exact application source under test.

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

The final Playwright recheck ran Chromium 148 against source `0db243d`. All 17 independent and
cross-feature cases passed at 1440 × 900, 1024 × 768, and 390 × 844 without document overflow.
Reduced motion left zero active animations and zero-duration animations/transitions. Repeater
history stayed tab-owned after every request edit, navigation, reload, a second task tab, and both
tab closures. The audit also exercised real Scanner and Intruder runs, delayed Intercept
acknowledgements, Authz retargeting, reversible API-key/allowlist actions, unconfigured Vault
behavior, and every Settings section. No unexpected console, page, HTTP, or external-request errors
were reported.

Chrome DevTools Protocol metrics covered three fresh 240-request proxy bursts and the resulting Map
hydration and gestures. The 1440 × 900 History window stayed bounded at 82 rendered rows and 4,825
DOM nodes, with no long tasks or visible stuck busy state.

See [`verification.md`](verification.md) for the revision, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
