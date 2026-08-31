# UI audit screenshots

The **After** screenshots validate only the audited runtime source identified by
`application_source.runtime_sha256` in [`browser-audit.json`](browser-audit.json). See
[`verification.md`](verification.md#current-source-applicability) for whether that retained
evidence applies to the current runtime source; never carry it forward across a runtime-source
change.

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

The retained exact-source Playwright recheck ran Chromium 148 and all 26 independent and
cross-feature cases passed. Dedicated viewport and control-reachability sweeps at 1440 × 900,
1024 × 768, and 390 × 844 found no document overflow.
Reduced motion left zero active animations and zero-duration animations/transitions. Repeater
history stayed tab-owned after every request edit, navigation, reload, a second task tab, and both
tab closures, including when its localStorage cleanup ledger was forced unavailable. The audit also
exercised OOB draft retention across interaction refresh, real Scanner and Intruder runs, delayed
Intercept acknowledgements, Authz retargeting, mutation rejection/retry focus, blocked busy-modal
navigation, reversible API-key/allowlist actions, unconfigured Vault behavior, and every Settings
section. A direct API mutation also proved the visible Allowlist pane reconciles another client's
addition and deletion over SSE without eagerly loading the API module. No unexpected console, page,
HTTP, or external-request errors were reported.

Chrome DevTools Protocol metrics covered three fresh 240-request proxy bursts and the resulting Map
hydration and gestures. Exact timing, bounded-DOM, long-task, and interaction measurements are
retained in `browser-audit.json` with the runtime-source digest.

See [`verification.md`](verification.md) for the source identity, feature matrix, measurements, failure
injections, competitive review, and intentionally deferred ideas.
