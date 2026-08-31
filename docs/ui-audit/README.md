# UI audit screenshots

The **After** screenshots validate the exact release-candidate runtime source identified by
`application_source.runtime_sha256` in [`browser-audit.json`](browser-audit.json). They were captured
from a fresh build based on
[`00cc339`](https://github.com/Veyal/interseptor/commit/00cc3392676b1fb20567541dc36d648506c85067)
plus the gate review fixes in this change. The screenshots and measurements remain scoped to that
digest; any later runtime-source change requires a fresh run.

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

The exact-target Playwright recheck ran Chromium 148. All 26 independent
and cross-feature cases passed at 1440 × 900, 1024 × 768, and 390 × 844 without document overflow.
Reduced motion left zero active animations and zero-duration animations/transitions. Repeater
history stayed tab-owned after every request edit, navigation, reload, a second task tab, and both
tab closures, including when its localStorage cleanup ledger was forced unavailable. The audit also
exercised OOB draft retention across interaction refresh, real Scanner and Intruder runs, delayed
Intercept acknowledgements, Authz retargeting, reversible API-key/allowlist actions, unconfigured Vault
behavior, and every Settings section. No unexpected console, page, HTTP, or external-request errors
were reported.

Chrome DevTools Protocol metrics covered three fresh 240-request proxy bursts and the resulting Map
hydration and gestures. Exact timing, bounded-DOM, long-task, and interaction measurements are
retained in `browser-audit.json` with the runtime-source digest.

See [`verification.md`](verification.md) for the source identity, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
