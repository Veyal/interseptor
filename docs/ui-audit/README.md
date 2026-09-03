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

[`verification.md`](verification.md) is the authoritative audit report for the retained evidence. It
owns the exact source and harness identities, browser-engine and feature matrices, failure
injections, accessibility results, performance measurements, and deferred work. The machine-readable
results remain in [`browser-audit.json`](browser-audit.json); this README owns only the screenshot
inventory above.
