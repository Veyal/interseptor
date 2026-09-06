# UI audit screenshots

The [2026-09-06 overall review](overall-review.md) covers the subsequent UI pass.
The full-audit screenshots below are historical evidence; their runtime identity
does not match the newer navigation and recovery changes.

The **After** screenshots validate only the audited runtime source identified by
`application_source.runtime_sha256` in [`browser-audit.json`](browser-audit.json). See
[`verification.md`](verification.md#current-source-applicability) for whether that retained
evidence applies to the current runtime source; never carry it forward across a runtime-source
change.

The retained **Before** screenshots are from repository baseline `ec1b79e`. The After set uses only
the audit's local loopback fixture and generic project records; neither set contains real request
data, personal data, or target information.

| Engine | Viewport | Surface | Before | After |
| --- | --- | --- | --- | --- |
| Chromium | 1440 × 900 | Proxy / History | [`before-1440x900-proxy.png`](before-1440x900-proxy.png) | [`after-1440x900-proxy.png`](after-1440x900-proxy.png) |
| Chromium | 1024 × 768 | Map | [`before-1024x768-map.png`](before-1024x768-map.png) | [`after-1024x768-map.png`](after-1024x768-map.png) |
| Chromium | 390 × 844 | Scanner | [`before-390x844-scanner.png`](before-390x844-scanner.png) | [`after-390x844-scanner.png`](after-390x844-scanner.png) |
| Firefox | 1440 × 900 | Proxy / History | same baseline | [`firefox/after-1440x900-proxy.png`](firefox/after-1440x900-proxy.png) |
| Firefox | 1024 × 768 | Map | same baseline | [`firefox/after-1024x768-map.png`](firefox/after-1024x768-map.png) |
| Firefox | 390 × 844 | Scanner | same baseline | [`firefox/after-390x844-scanner.png`](firefox/after-390x844-scanner.png) |
| WebKit | 1440 × 900 | Proxy / History | same baseline | [`webkit/after-1440x900-proxy.png`](webkit/after-1440x900-proxy.png) |
| WebKit | 1024 × 768 | Map | same baseline | [`webkit/after-1024x768-map.png`](webkit/after-1024x768-map.png) |
| WebKit | 390 × 844 | Scanner | same baseline | [`webkit/after-390x844-scanner.png`](webkit/after-390x844-scanner.png) |

The narrow Scanner pair shows the most visible geometry correction: the issue list and detail pane
stack instead of compressing the detail to an unreadable rail. History keeps its intentionally
horizontal, scrollable dense-table surface while its controls wrap inside the viewport.

## Verification summary

[`verification.md`](verification.md) is the authoritative audit report for the retained evidence. It
owns the exact source and harness identities, browser-engine and feature matrices, failure
injections, accessibility results, performance measurements, and deferred work. The machine-readable
results remain in [`browser-audit.json`](browser-audit.json), whose `engine_reports` retain every
engine's complete case matrix, performance metrics, and six core/Findings screenshots; this README
owns only the core screenshot inventory above.

The historical managed verification retains 24 focused custom-control and Settings captures
(23 primary captures and one forced-colors diagnostic) in
[`supplemental/`](supplemental/). They use a separate disposable loopback project and supplement,
rather than replace, the cross-browser core evidence above.

The current visual candidate is documented in the [visual redesign review](visual-redesign.md),
with UI-only evidence kept separate from historical full operational audits.
