# v2.17.0 desktop visual-audit checkpoint

A full managed browser audit was run against the working tree that contains the
v2.17.0 desktop changes: the native macOS shell, the redesigned panels, and the
fixes for the two defects found by the ported harness. The run took place on
2026-10-18 from 08:16:23Z to 08:24:13Z. It replaces
[post-release-v2.1.0](post-release-v2.1.0.md) as the checkpoint that
`TestUIVisualAuditEvidenceMatchesCurrentRuntime` and `tools/auditdigest` check.
The earlier evidence remains unchanged as historical evidence.

## What ran

```bash
CFFIXED_USER_HOME="$(mktemp -d)" \
  python3 scripts/ui_browser_audit.py --full --managed --output-dir <scratch>/run2
```

- Tools: Python 3.9.6 and Playwright 1.60.0, using the bundled Chromium
  (chromium-1223), Firefox 150.0.2 (firefox-1522) and WebKit (webkit-2287)
  builds. The host was macOS 27.0.1 on arm64, and the binaries were built with
  Go 1.26.5.
- Workload: the defaults, `--burst 240` and `--perf-runs 3`. For each engine,
  the harness built its own CGO-free candidate from a verified source snapshot.
  It started that candidate on parent-reserved random loopback ports with a
  sentinel-owned temporary project and removed everything afterwards. The run
  used only generic loopback and `example.com` fixtures.
- `CFFIXED_USER_HOME` is an environment-only workaround for a host problem. On
  macOS 27, Playwright's bundled Firefox resolves the TCC-protected
  `~/Library/Application Support/Firefox` directory and hangs at launch
  ([microsoft/playwright#42768](https://github.com/microsoft/playwright/issues/42768)).
  It does not change the harness or the product.

## Results

- **132/132 cases passed**, 44 each in Chromium, Firefox and WebKit.
- The run recorded no failures and no console, page, HTTP or external-request
  errors.
- The cases included the required viewports (1440×900, 1024×768 and 390×844),
  a reduced-motion context, the high-volume History/Map performance profile,
  and phone-width reachability for every dense panel.
- Reports: the [aggregate report](release-v2.17.0-desktop/visual/report.json)
  embeds the Chromium report and the per-engine reports, alongside
  [firefox.json](release-v2.17.0-desktop/visual/firefox.json) and
  [webkit.json](release-v2.17.0-desktop/visual/webkit.json). The published JSON
  differs from the original output only in local run paths, which were made
  relative. The manifest records the hash of each original report.
- Screenshots: 18 PNGs, six per engine (Proxy at 1440×900, Map at 1024×768,
  Scanner at 390×844, and Findings at all three sizes), in
  [visual/screenshots](release-v2.17.0-desktop/visual/screenshots/).

## Identities

| Item | Value |
| --- | --- |
| Runtime digest (773 files) | `648638510564eeba47b20a56aad74a46f5439f37fe4c6bd93aa76257aad46ece` |
| Harness `scripts/ui_browser_audit.py` | `50ad7ce2e3db2790955b93b534f8763020b6ccb76e8fabef658c7f8ac0bfb071` |
| Reference binary (`CGO_ENABLED=0 go build -trimpath ./cmd/interseptor`) | `09255857f95d15cde237576ac32c07d3e33516e2403bc8b13a1221d9c5168df0` |
| Worktree base commit | `f2b9c7c9a7b6df77f78aa27353fb0894234d62b4` |

The reference binary still reports `interseptor v2.16.0` because the version
string had not been bumped when the audit ran. The runtime digest is the
binding identity. It uses the same selection and hashing as the Go test: every
tracked or non-ignored non-test file under `go.mod`, `go.sum`, `cmd/` and
`internal/`. The harness builds and deletes its own binary for each engine, so
the reference build is recorded only to identify the binary.

## Harness changes in this release

`scripts/ui_browser_audit.py` was ported to the redesigned UI. The port covers:

- bottom-dock and command-palette navigation at phone width
- Scanner tools and History filter popovers
- opt-in Findings editing, including the inline new-finding row and the Report
  view
- the flow drawer, cascading row menu and deferred Drop
- shared in-flight reads

The harness also gained:

- a Findings properties-rail overlap check
- a History DOM-per-row budget that matches the current row
- a fixture backlog large enough for the burst pass

Its first runs found two product defects, which were fixed before this run:

- the History scroll position reset after a panel round trip (`js/canvas.js`)
- the Findings properties rail painted over the finding at narrow widths
  (`findings.css`)

This run made no further changes to the harness.

## Files

The [manifest](release-v2.17.0-desktop/manifest.json) records the runtime,
harness and binary identities, the audit command and time window, the tool
versions, and the SHA-256 of every retained file. The retained files are the
reports, the screenshots, the executed harness copy
(`probes/ui_browser_audit.py`) and the assembly script
(`probes/assemble-manifest.py`). That script refuses to write a checkpoint
unless every engine passed and the run's runtime and harness digests equal the
current tree.

To repeat this audit, run it again and assemble a new checkpoint directory. Do
not relabel this one or edit its hashes by hand.
