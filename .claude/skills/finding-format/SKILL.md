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
- ordered affected `targets` with primary `target`, environment, CWE, and tags;
- a CVSS v4.0 vector, calculated score, and matching severity;
- ordered typed reproduction/evidence `blocks`;
- `fix` and `retest`;
- `confidence`, status, `proofReview`, and verification instructions when required.

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

Set `proofReview.execution` to `prerequisite_only` or `not_executed`, with an explicit `reason`,
when impact has not been demonstrated. A later status-only patch must not bypass this state.
Readiness separately checks annotated action, result, and control artifacts. Narrative text steps
do not substitute for evidence. `proofReview.evidence` can map several checks to one captured
request/response or browser image; never add duplicate traffic merely to fill role slots.

## Target compatibility and merge

`targets` is ordered JSON. Keep scalar `target` as the first URL and retain all other targets on a
legacy scalar edit. Old rows are projected as one target without rewriting their original text.
The read-only compatibility projection is reconstructed after archive restore or merge.

Target `flow_ids` refer to captured flows; `image_hashes` associate declared browser captures.
Readiness requires annotated attached evidence, except documented `setup`/`chain` target exceptions.
Persist missing peer references and remap both target and review evidence on merge. A rename or
reorder must not bind an orphan peer ID to unrelated local evidence. Batch reference existence
lookups to avoid one SQL query per target/flow pair during list refreshes.

Environment values are production, staging, development, testing, local, and legacy prod. Reject
unsupported writes. Never infer that an old local finding originally meant development.

Full archives carry SQLite metadata. PDF is the existing HTML print-to-PDF workflow, not a direct
API format. Keep Markdown, HTML, JSON, archive snapshots, merge, REST, and MCP tests aligned.

## Keep aligned

When the contract changes, update all of:

- `internal/store/findings.go` and migrations;
- `internal/control/findings.go`;
- `internal/control/ui/js/findings.js` and the writing guide;
- `internal/mcp/finding_format.go` plus tool schemas;
- `internal/report/report.go`;
- `docs/findings-and-reporting.md` and tests.


### History and report gates

Every canonical finding mutation must append its revision in the same transaction, with a baseline
for legacy findings. Snapshot both the body and legacy attachment rows; restore must preserve their
union and explicit missing markers. Delete verification rows transactionally after snapshotting.
HTTP boundaries stamp API-client attribution; never imply a verified named human. History export
summaries omit field values and arbitrary reasons. Final report mode consumes shared store readiness
checks; drafts remain available. Capability declarations are review evidence, not automated proof.
