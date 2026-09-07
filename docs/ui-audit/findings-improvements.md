# Findings improvements and issue review — 2026-09-07

Reviewed the twelve open improvement requests, #65–76. They fit Interseptor's
existing evidence workflow: keep findings accurate, recover edits, preserve
evidence relationships, and make review decisions easier to inspect. Advanced
controls remain in the relevant editor section or disclosure, using the shared
custom controls.

## Delivered scope

| Issues | Local behavior | Limits |
|---|---|---|
| [#65](https://github.com/Veyal/interseptor/issues/65), [#66](https://github.com/Veyal/interseptor/issues/66), [#67](https://github.com/Veyal/interseptor/issues/67) | Previously implemented capability-based proof guidance, multiple affected targets, and explicit development/testing environments are retained. | Readiness evaluates declarations and artifact completeness; the reviewer remains responsible for interpreting evidence. |
| [#68](https://github.com/Veyal/interseptor/issues/68) | MCP initialization exposes contract version/hash; declared mismatches receive reconnect diagnostics. A capabilities endpoint lists the live supported finding fields. | A client that never declares its cached version/hash cannot be diagnosed automatically. An old running binary or external client cache still requires restart/reconnect. |
| [#69](https://github.com/Veyal/interseptor/issues/69) | Screenshot ingestion provenance stays separate from editable browser/device classification, with classification attribution and history. | Attribution identifies the writing boundary, not a verified human identity. Legacy ingestion time remains unknown. |
| [#70](https://github.com/Veyal/interseptor/issues/70), [#76](https://github.com/Veyal/interseptor/issues/76) | Claim-to-evidence checks provide actionable review links. Final report export rejects incomplete findings; Draft remains available. | Heuristics flag obvious unsupported wording; these checks cannot independently prove a security claim. |
| [#71](https://github.com/Veyal/interseptor/issues/71), [#72](https://github.com/Veyal/interseptor/issues/72) | A passive History inspector compares selected captures, role labels, response fingerprints, cookie observations, and candidate transitions. Roles start Unassigned. | No request replay or active cross-identity testing. No inferred login/MFA success, browser cookie decisions, dynamic-body normalization, or automatic differential finding attachment. |
| [#73](https://github.com/Veyal/interseptor/issues/73) | CVSS v4 preview and base-metric controls share the backend evaluator. Apply saves vector and matching severity together. | Optional metrics can be supplied in the vector. Legacy vectors remain readable as drafts; final readiness requires valid v4 and matching severity. |
| [#74](https://github.com/Veyal/interseptor/issues/74) | Cleanup previews exact duplicates and optional path templates. Apply combines evidence links and notes while preserving method, role, scheme, and variant distinctions. | Templates are opt-in. Legacy URL splitting is conservative, and shared legacy associations still need review. |
| [#75](https://github.com/Veyal/interseptor/issues/75) | Append-only revisions, field diffs, deleted-finding recovery, and a value-free audit summary. Restore creates a new revision and preserves evidence relationships. | Independently purged raw flows remain missing. Peer merges create local history without importing full peer lineage. Historical images remain retained; there is no automatic revision purge. |

## Defects corrected during verification

- Target templates now pass through a final deduplication step: selecting the
  same `{id}` template for two endpoints produces one target with both evidence
  associations in one Apply operation.
- Restoring legacy or missing flow references preserves their explicit API
  projection without inserting unsafe attachment rows that could bind an old
  identifier to unrelated current traffic.
- Malformed request targets cannot bypass query-value redaction in the passive
  inspector.
- Claim, CVSS, and revision-history disclosures retain stable identities during
  refreshes. Claim fields retain capability-specific focus.
- A deferred Findings refresh waits until a pointer click finishes. A WebKit
  trace reproduced the earlier failure: field focusout remounted the pressed
  Review link before its click could fire.

## Validation

All 21 functional browser cases passed: seven each in Chromium, Firefox, and
WebKit. The cases cover revision inspection/restore, target-cleanup Cancel and
Apply, screenshot classification, claim controls, Final rejection and Draft
download, CVSS Apply, deleted-finding recovery, passive session inspection,
theme switching, and hidden native select adapters.

- [Functional report](findings-improvements/report.json) and
  [exact executed probe](findings-improvements/probe.py)
- [Runtime, binary, and artifact hashes](findings-improvements/manifest.json)

The 36 functional screenshots are preserved unchanged, including captures
during transitions. A separate visual pass provides settled feature views and
an additional restore check after a newer edit. All 18 additional checks passed
(six per engine), retaining 30 more screenshots. Restore changed the title
back, retained both target-to-flow associations, and increased the revision
count from five to six in every engine.

- Visual/recovery reports: [Chromium](findings-improvements/visual/chromium.json),
  [Firefox and WebKit](findings-improvements/visual/firefox-webkit.json)
- [Exact visual probe](findings-improvements/visual/probe.py)
- [Expanded revision diff](findings-improvements/visual/screenshots/chromium-desktop-light-expanded-before-after-diff.png)
- [Mobile claim controls](findings-improvements/visual/screenshots/chromium-mobile-light-claim.png),
  [dark CVSS controls](findings-improvements/visual/screenshots/chromium-mobile-dark-calculator.png),
  [mobile cleanup preview](findings-improvements/visual/screenshots/chromium-mobile-light-cleanup.png)

Runtime SHA-256:
`955814eda9dff97c129f2de7562f9f0a15d5d6c0117c0bd11e5bfafd1ba2d27b`
across 276 files. The tested `2.0.10-local` binary has SHA-256
`5e7fab26f77148ade6a7e970674b1aab29d2e3c3faba2de5d9e40a1f0dad7106`.

Local checks passed without skips: `go test ./...` and `go test -race ./...`
(47 packages each), `go vet ./...`, syntax checks for all 30 JavaScript modules,
documentation checks, and the no-cgo `make build`.
The binary was installed locally with a backup; the operator's running proxy
and project were not restarted or changed.

Replay the functional probe from the repository root after `make build`, using
an unused output directory and installed Python/Playwright engines:

```sh
QA_OUT=/tmp/interseptor-findings-improvements-replay python3 docs/ui-audit/findings-improvements/probe.py
QA_OUT=/tmp/interseptor-findings-visual-replay python3 docs/ui-audit/findings-improvements/visual/probe.py
```

The probe verifies exact source and binary hashes before launching a disposable
candidate. Set `QA_REPO` only when replaying from outside the repository root.

The browser scope is the affected Findings and passive History journeys using
generic data and isolated loopback fixtures. It does not certify live target
behavior or every unrelated application feature. Earlier audit collections
remain historical.

At audit completion, the changes were local and uncommitted; no GitHub issues
had been closed. Release validation is recorded separately.
