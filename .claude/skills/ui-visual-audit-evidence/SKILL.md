---
name: ui-visual-audit-evidence
description: How the retained visual-audit evidence gate works, why any runtime source change makes TestUIVisualAuditEvidenceMatchesCurrentRuntime fail, and the honest ways to refresh or verify UI visuals. Use when that test is red, when regenerating docs/ui-audit evidence, or when a UI change needs a quick screenshot check.
---

# UI visual-audit evidence

## The gate

`internal/control/ui_browser_audit_test.go` (`TestUIVisualAuditEvidenceMatchesCurrentRuntime`)
hashes every tracked non-test file under `go.mod`, `go.sum`, `cmd/`, `internal/` (same file
selection as `runtime_source_paths()` in `scripts/ui_browser_audit.py`) and compares it with
`runtime.runtime_sha256` in `docs/ui-audit/post-release-v2.1.0/manifest.json`. Any change to any
runtime file — even a one-line CSS edit — turns the test red by design. Treat that as expected
drift until a genuine audit is re-run; never patch the hash by hand.

## What the manifest is (and is not)

- `scripts/ui_browser_audit.py` only ever writes `browser-audit.json`. The retained
  `manifest.json` (runtime/binary identities + per-file evidence hashes) was assembled by a
  one-off probe kept under `docs/ui-audit/<checkpoint>/probes/`. That probe pins the exact
  `FROZEN` runtime digest and `BINARY` hash it was executed against and refuses to run otherwise.
- The checkpoint docs say: to repeat, create a **new copy** with current source/binary hashes;
  never relabel old reports as a later execution.

## Honest ways forward

1. Leave the test red and say so in the report (fine for iterative UI work).
2. Full refresh: fix/run `scripts/ui_browser_audit.py --full --managed` (Chromium+Firefox+WebKit)
   until it passes with screenshots, write a new manifest from that run's real artifacts (report,
   probe, ≥8 screenshots, harness hash) under a new checkpoint directory, and point the test at it.
3. Quick visual verification while iterating: build the binary and run a small Playwright script
   that starts it on private ports with a throwaway `--data-dir`, seeds loopback traffic through
   the proxy, and screenshots each panel in both themes at 1440×900 and 390×844 (select tabs via
   `#mobileToolSelect` at ≤720px because the desktop tab rail is hidden there). The Read tool
   renders PNGs, so agents can inspect results without a browser session.

## Harness gotchas

- Custom-select menu ids (`uiSelectList<N>`) are positional; resolve them from the trigger's
  `aria-controls` (`#<selectId>Ui`) rather than hard-coding N.
- At ≤720px `.tab[data-tab=…]` is `display:none`; use the mobile select.
- `chrome-devtools-axi` may attach to the operator's live Chrome; prefer headless Playwright for
  verification so no real tabs are disturbed.
