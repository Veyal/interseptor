# Repeater Render and History hints QA — 2026-09-07

Repeater now offers **Render** for HTML responses, using the same sandboxed body
preview as Proxy History. History rows no longer repeat the click/selection
instructions; their inspection and selection actions remain available.

All 24 functional browser cases passed. A separate settled-theme visual check
passed in all three engines. The retained evidence contains 18 app screenshots.

| Engine | Functional cases | Settled dark mobile check |
|---|---:|---|
| Chromium | 8 passed | Passed |
| Firefox | 8 passed | Passed |
| WebKit | 8 passed | Passed |

The functional journey covers HTML Send → Render, body-only sandbox isolation
with scripts disabled, switching Raw/Pretty/Decoded/Render, replacement HTML,
JSON fallback to Pretty, clearing previews during pending sends and captured
502 responses, per-tab response ownership, Repeater History, and Proxy History
Render parity. Hovering, inspecting, and Ctrl-selecting History rows does not
restore the removed instructional hint.

Screenshots cover 1440×900, 1024×768, and 390×844 layouts. The mobile response
controls and HTML document remain reachable in the nested scroll area. The
functional captures are preserved unchanged, including theme transitions;
use the separate visual captures below to assess the settled dark theme.
That pass uses the app's theme button, checks the computed control background
against `--bg3`, and captures after finite transitions settle.

## Evidence and replay

- [Functional report](repeater-render/report.json) and [executed probe](repeater-render/probe.py)
- [Settled-theme report](repeater-render/visual/report.json) and [executed probe](repeater-render/visual/probe.py)
- [Runtime and artifact hashes](repeater-render/manifest.json)
- [Desktop response](repeater-render/screenshots/firefox-1024-light-repeater-response-modes.png)
- [Mobile light](repeater-render/screenshots/webkit-390-light-repeater-render.png)
- [Mobile dark](repeater-render/visual/screenshots/chromium-390-dark-repeater-render-settled.png)

Runtime SHA-256: `3a4534b279d9244ccffe636fb0c9c0e0663f02a3aecb311b37baa95a6f2361ec`
across 259 files. The manifest binds both exact executed probes, reports, and
screenshots. Original temporary paths in reports remain unchanged.

From the repository root, with Go, Python/Playwright and its three engines installed:

```sh
QA_OUT=/tmp/interseptor-repeater-functional-replay python3 docs/ui-audit/repeater-render/probe.py
QA_OUT=/tmp/interseptor-repeater-visual-replay python3 docs/ui-audit/repeater-render/visual/probe.py
```

Choose unused output directories; the probes refuse to replace existing evidence.
They use isolated candidates and generic loopback fixtures. The operator's active
proxy and project are not used. Earlier audit collections remain historical.

## Scope and validation

This verifies passive HTML preview and the affected UI journeys. It does not
certify live target behavior, JavaScript-driven pages, or all application features.
The behavioral regression test additionally covers overlapping response reads,
late completion after a tab switch, and explicit opt-in for oversized HTML.

Local checks passed without skips: `go test ./...`, `go test -race ./...`,
`go vet ./...`, JavaScript syntax checks, documentation generation/checks,
`git diff --check`, and the no-cgo `make build` producing `2.0.10-local`.
