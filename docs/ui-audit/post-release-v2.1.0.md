# v2.1.0 post-release visual verification

The maintenance build advances the development fallback to `2.1.0`; local builds
report `2.1.0-local`. Application behavior is unchanged from the
[release verification](release-v2.1.0-verified.md), which retains the broader
Findings, CVSS, export, deletion, and project recovery context.

All 18 focused checks passed in Chromium, Firefox, and WebKit with default
motion. The six cases per engine cover cleanup preview, restoration of a distinct
older revision with its target links, custom claim/CVSS controls, the deleted
findings dialog, passive session inspection, and reachable 390px light/dark
controls. The [report](post-release-v2.1.0/visual/report.json) retains 30 settled
screenshots from disposable loopback fixtures using generic example data.

The [manifest](post-release-v2.1.0/manifest.json) records the exact executed probe,
frozen helper, source/binary identities, and every artifact hash. Published JSON
reports normalize only local probe and screenshot paths to relative artifact
links; original report hashes and the transformation are recorded. All other
values and all probe, helper, and PNG bytes are unchanged. Earlier release
evidence remains historical and intact.

To repeat the focused probe, create a new copy and supply the repository, current
source/binary hashes, and owned temporary fixture paths before execution. The
retained probe references a generic temporary frozen-helper path; supply the
retained helper at that path or adapt the new copy. Keep the executed copy and
its results together. Never relabel these reports as a later execution.
