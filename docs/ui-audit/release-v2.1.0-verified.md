# v2.1.0 final release verification

The retained capture includes the finding deletion/evidence-write guards and
revision-input validation made after the earlier final-release capture. All 27
checks passed in Chromium, Firefox, and WebKit with default motion.

This capture predates the MCP screenshot-help, evidence-block comment, and Go
import-grouping corrections. Its source identity remains unchanged; a new
current-source visual run is required before release acceptance.

The six visual checks per engine cover cleanup preview, restoring a distinct
older revision with target links intact, custom claim/CVSS controls, deleted
findings, passive session inspection, and reachable 390px light/dark controls.
Thirty settled screenshots accompany the [visual report](release-v2.1.0-verified/current-1083cd00/visual/report.json).

The three additional journeys per engine verify:

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

The [manifest](release-v2.1.0-verified/manifest.json) records the exact executed
probes, their fixture dependency, source/binary identities, and every published
artifact hash. Published JSON reports are **path-normalized copies**: only local
probe, screenshot, and download paths become relative artifact links. All
results, assertions, captured file hashes, and other values remain unchanged.
Each report's original SHA-256 is retained in `publication_reports`. Exact raw
reports, including two unsuccessful Chromium locator/timing attempts before the
accepted saved-state retry, remain in the isolated evidence root. The earlier
release capture remains unchanged in this directory and is identified in the
manifest as historical evidence.

Runtime SHA-256: `1083cd00e5eca944321c6af51b95118ddf32120055a9cf31379ca1ef3d4122a5`
across 276 files. Tested CLI: `2.0.10-local`, SHA-256
`c02ef1a3d1fa887359ebefd3edd4d2ac41e39928def23c3f1ae46e50c7d9ba12`.
Release artifacts receive `2.1.0` through linker flags. This is focused release
verification; the broader feature audit remains in the earlier retained reports.
