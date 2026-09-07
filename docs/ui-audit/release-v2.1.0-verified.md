# v2.1.0 final release verification

Codex Terra passed 18 fresh final-source visual checks: six cases in each of
Chromium, Firefox, and WebKit with default motion and no failures. This run
follows the MCP screenshot-help, evidence-block comment, and Go import-grouping
corrections; application logic is unchanged from the prior 27-check run. It also
includes the finding deletion/evidence-write guards and revision-input validation.

The six visual checks per engine cover cleanup preview, restoring a distinct
older revision with target links intact, custom claim/CVSS controls, deleted
findings, passive session inspection, and reachable 390px light/dark controls.
Thirty settled screenshots accompany the [visual report](release-v2.1.0-verified/current-75052a98/visual/report.json).

The historical [27-check capture](release-v2.1.0-verified/current-1083cd00/visual/report.json)
covered the same six visual cases plus three behavioral journeys per engine.
Those journeys were not rerun on the final source; they verified:

- A delayed CVSS Apply preserves a newer vector across editor remounts; a failed
  Apply retains that vector for retry.
- Export waits for a pending save and includes its new value. Both Draft and
  Final export reject unresolved failed edits without requesting an old report.
- Discarding a failed Apply restores the saved vector, clears its owned drafts,
  and permits Done, actual project creation/switching back, and export.

The [journey report](release-v2.1.0-verified/current-1083cd00/journeys/report.json) includes six
downloaded exports from disposable loopback fixtures. No engagement or live
project data was used. Project switching used a separate fixture that permits
restart; the standard managed visual fixture intentionally disables switching.

The active [manifest](release-v2.1.0-verified/manifest.json) records the final
visual run, its exact executed probe and frozen helper, source/binary identities,
and all 36 artifact hashes (30 PNGs, four reports, and two Python files). Its
`historical_runs` retains the previous 27-check manifest and original-source
evidence identity without relabeling either as current coverage. Published JSON
reports are **path-normalized copies**: only local
probe, screenshot, and download paths become relative artifact links. All
results, assertions, captured file hashes, and other values remain unchanged.
Each report's original SHA-256 is retained in its manifest
`publication_reports`. Raw originals and prior unsuccessful locator/timing
attempts remain local; historical reports, probes, screenshots, and downloads
remain unchanged in this directory.

Runtime SHA-256: `75052a9827615f6b40f4cf31a5b3f5084617fbfd9e3229b3c7322e32a83c90fb`
across 276 files. Tested CLI: `2.0.10-local`, SHA-256
`7dd54c9614c0b42d046cd14e58afeaffe9366c2c524ffe2c1dd0bfe4be1b59d6`.
Release artifacts receive `2.1.0` through linker flags. This is focused release
verification; the broader feature audit remains in the earlier retained reports.

For reproduction, use the exact [executed probe](release-v2.1.0-verified/current-75052a98/probes/interseptor-findings-visual-final-cf601a6d-probe-ui-settled.py)
and [frozen helper](release-v2.1.0-verified/current-75052a98/probes/frozen-helper.py).
The probe looks up the helper at `/tmp/interseptor_findings_improvements_qa_frozen.py`;
place its unchanged bytes there before execution. Run from the source root (or
set `QA_REPO`); it loads `scripts/ui_browser_audit.py` from that root. Set
`QA_BINARY` to the matching dedicated `2.0.10-local` binary and `QA_OUT` to a new
owned temporary directory. The probe checks source and binary hashes; use the
recorded source, harness, binary, probe, and helper identities in the manifest.
The environment must provide Python Playwright and Chromium, Firefox, and WebKit.
Use only disposable loopback fixtures; reproduction requires no live projects or
targets. The outer executor owns post-publication tests, race checks, vet,
docscheck, and the no-cgo build.
