---
name: finding-format
description: Keep Findings evidence-first, reproducible, provenance-aware, and identical across UI, REST, MCP, and exports.
---

# Finding format

Use the canonical envelope in `docs/findings-and-reporting.md`. Do not invent a separate AI template
or file report prose as one markdown blob.

## Envelope

A report-ready finding contains:

- claim: `title`, concise `summary`;
- risk: `impact`, `why`, `severity`;
- affected object: `target`, optional `environment`, `cwe`, `cvss`, and tags;
- ordered typed reproduction/evidence `blocks`;
- `fix` and `retest`;
- `confidence`, status, and verification instructions when required.

Title-only drafts remain valid. Read `readiness.stage` and `readiness.gaps`; do not treat legacy
`ready` as proof that the vulnerability is true.

## Blocks

Prefer the structured `blocks` array. Legacy `body` is a JSON string kept for old clients. Never send
both.

Allowed roles are `context`, `setup`, `baseline`, `action`, `result`, `control`, `observation`, and
`retest`. `before` maps to `baseline`; `after`/`proof` map to `result`. Unknown roles are errors.

`Before → Action → After` is only the **Differential proof** preset. Choose roles that match the
actual issue:

- authorization/state change: baseline → action → result;
- injection/reflection: action → result;
- exposure/misconfiguration: observation → result;
- custom multi-step cases: the smallest accurate ordered sequence.

## Evidence

Every report-ready finding needs non-missing flow or image evidence. Every evidence block needs a
string `proof` explaining the exact claim it establishes.

- Attach an Interseptor flow whenever the relevant request was captured.
- Prefer a real `browser_screenshot` when visual state proves the issue.
- Use `flow_preview` for a generated HTTP report image and retain `sourceFlowId`.
- Never describe a generated flow preview as a real browser screenshot.
- Caption identifies the artifact; proof identifies the security-relevant observation.
- Redact secrets and unrelated private data from screenshots and prose.

Use `get_finding` before editing, `add_finding_poc` for flows, `add_finding_image` for real screenshots,
and `render_flow_preview` for generated HTTP images. Do not put base64 or local paths in block JSON.

## AI integrity

Keep AI interpretation separate from raw evidence. If execution, callback, or state change was not
observed, use `needs_verification`, say **NOT confirmed**, and provide exact
`verificationInstructions`. Confidence is separate from severity and must be `tentative`, `firm`, or
`certain`.

## Keep aligned

When the contract changes, update all of:

- `internal/store/findings.go` and migrations;
- `internal/control/findings.go`;
- `internal/control/ui/js/findings.js` and the writing guide;
- `internal/mcp/finding_format.go` plus tool schemas;
- `internal/report/report.go`;
- `docs/findings-and-reporting.md` and tests.
