# Findings assessment QA — 2026-09-07

Focused verification of GitHub issues [#65](https://github.com/Veyal/interseptor/issues/65),
[#66](https://github.com/Veyal/interseptor/issues/66), and
[#67](https://github.com/Veyal/interseptor/issues/67). All 25 browser cases passed against
the final runtime source, with 12 retained app screenshots.

| Engine | Passed cases | Screenshots |
|---|---:|---:|
| Chromium | 9 | 4 |
| Firefox | 8 | 4 |
| WebKit | 8 | 4 |

The journey covers adding, editing, reordering and removing targets; setup exceptions;
linking evidence to a non-primary target; environment selection and invalid-value rejection;
impact review and evidence mapping; calculated CVSS; image-origin classification;
reload persistence; non-primary target search; Markdown, HTML and JSON downloads;
390-pixel layouts in both themes; custom dropdown keyboard/Escape behavior; and retention
of a failed Review save with Retry. Chromium also printed the downloaded HTML to PDF;
`pdftotext` verified that affected targets remained present.

Desktop captures are 1440×900. Mobile captures are 390×844. The final visual pass removed
a duplicate disclosure icon and a stale, redundant image-origin label.

## Evidence and replay

- [Browser results](finding-assessment/report.json)
- [Executed portable probe](finding-assessment/probe.py)
- [Source and artifact hashes](finding-assessment/manifest.json)
- [Desktop evidence editor](finding-assessment/screenshots/chromium-light-desktop-edit.png)
- [Mobile Review](finding-assessment/screenshots/webkit-dark-390x844-edit.png)
- [Printed PDF](finding-assessment/downloads/findings-print.pdf)

Runtime SHA-256: `1cc3ea14dd0021dccba889b29f439b7c5112a4202041c670b3dde5cd65042b80`
across 259 runtime files. The manifest also binds the audit harness and exact executed
probe. Reports retain their original temporary output paths; the manifest maps the
copied evidence to its repository location without rewriting the original results.

From the repository root, with Go, Python/Playwright, its three browser engines, and
`pdftotext` installed:

```sh
QA_OUT=/tmp/interseptor-finding-assessment-replay python3 docs/ui-audit/finding-assessment/probe.py
```

Choose an unused output directory. The probe refuses to replace an existing directory.
It creates isolated candidates through `scripts/ui_browser_audit.py`, retaining both
loopback listener descriptors until the owned child has stopped. It never connects to
the operator's running project.

## Scope and limits

All records use generic local fixtures. The uploaded one-pixel image is synthetic test
data for exercising the classification control; it is not evidence of a security claim.
The captured app screenshots are real browser screenshots. This run verifies Findings
workflows, not live target vulnerabilities or the entire application's operational scope.
Earlier audit collections remain historical and are not relabeled as current evidence.

Store/API/MCP tests additionally cover CVSS severity bands, prerequisite-only status
enforcement, generated-image provenance, full archive restore, merge reference remapping,
missing references that collide with local IDs, legacy scalar targets, atomic validation,
and failed structural target saves. Text steps describe a narrative and do not replace
captured action/result/control evidence.

Final local checks passed without skips: `go test ./...`, `go test -race ./...`,
`go vet ./...`, JavaScript syntax checks, `docscheck generate/check`, `git diff --check`,
and the no-cgo `make build` producing `2.0.10-local`.
