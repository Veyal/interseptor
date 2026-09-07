# v2.1.0 final release verification

The final source includes the merge-provenance, CVSS draft/discard, export-save,
and grouped-report readiness fixes made after the [initial candidate audit](release-v2.1.0.md).
All 27 checks passed in Chromium, Firefox, and WebKit with default motion.

The six visual checks per engine cover cleanup preview, restoring a distinct
older revision with target links intact, custom claim/CVSS controls, deleted
findings, passive session inspection, and reachable 390px light/dark controls.
Thirty settled screenshots accompany the [visual report](release-v2.1.0-verified/visual/report.json).

The three additional journeys per engine verify:

- A delayed CVSS Apply preserves a newer vector across editor remounts; a failed
  Apply retains that vector for retry.
- Export waits for a pending save and includes its new value. Both Draft and
  Final export reject unresolved failed edits without requesting an old report.
- Discarding a failed Apply restores the saved vector, clears its owned drafts,
  and permits Done, actual project creation/switching back, and export.

The [journey report](release-v2.1.0-verified/journeys/report.json) includes six
downloaded exports from disposable loopback fixtures. No engagement or live
project data was used. Project switching used a separate fixture that permits
restart; the standard managed visual fixture intentionally disables switching.

The [manifest](release-v2.1.0-verified/manifest.json) records the exact executed
probes, their fixture dependency, source/binary identities, and every published
artifact hash. Published JSON reports are **path-normalized copies**: only local
probe, screenshot, and download paths become relative artifact links. All
results, assertions, captured file hashes, and other values remain unchanged.
Each report's original SHA-256 is retained in `publication_reports`. Exact raw
reports and unsuccessful probe attempts remain local; the original candidate
evidence is unchanged. The accepted reports do not represent those earlier
locator, timing, export-format, or fixture-setup failures as passing runs.

Runtime SHA-256: `5ab87841e71bfc9cb1e7693b96fc97e5cde63fec1e037a8efccde05b9bb8e52b`
across 276 files. Tested CLI: `2.0.10-local`, SHA-256
`b33246d19f37870dbb9cda1aa856c08381e08bb9e41d423161fe55a879049996`.
Release artifacts receive `2.1.0` through linker flags. This is focused release
verification; the broader feature audit remains in the earlier retained reports.
