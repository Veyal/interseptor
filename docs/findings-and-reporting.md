# Findings and reporting

Findings keep a reviewed claim, affected targets, evidence, remediation, and review state in one
project record. The same record appears in the app, API, and exported reports. A draft can remain
incomplete; a Final report requires the relevant readiness checks to pass.

## Read and edit a finding

Open **Report → Findings**, search or filter the list, and select a record. Its sections stay linked
within the workspace:

- **Overview** — the claim, risk, affected targets, environment, and classification.
- **Evidence** — ordered steps, captured HTTP, and attached images with proof annotations.
- **Remediation** — the proposed fix and retest guidance.
- **Review** — verification state, readiness gaps, CVSS, and revision history.

Use the edit action for the section you want to change. Attached HTTP evidence opens inline so you
can read the request and response without leaving the finding. **Done** returns to reading mode.
A failed save keeps the draft with **Retry**; resolve it before exporting, restoring a revision,
or switching projects.

For a report, open **Export** and choose **Final** or **Draft**. Final checks apply to the findings
included in the export. Draft remains available for incomplete work. Readiness checks assess
record completeness and declared review state; they do not independently prove a vulnerability.

Continue with [readiness](#capability-based-report-readiness),
[multiple targets](#multiple-affected-targets), [screenshot provenance](#screenshot-provenance),
or [revision recovery](#revision-history-and-recovery).

## Canonical finding envelope

The canonical machine shape is the normal finding JSON returned by `GET /api/findings/{id}`. New
clients should send a `blocks` array and ordered `targets`. The legacy `body` JSON string remains accepted for backward
compatibility, but a request must not send both.

```json
{
  "title": "Example response needs review",
  "summary": "A recorded response contains an unexpected example field.",
  "severity": "Medium",
  "status": "needs_verification",
  "confidence": "tentative",
  "environment": "development",
  "targets": [
    {"url": "https://example.com/records", "methods": ["GET"], "role": "reader", "relation": "affected", "flow_ids": [101]},
    {"url": "https://example.com/workspace", "methods": ["GET"], "relation": "setup", "evidenceException": "Preparation only; the observation concerns the records endpoint."}
  ],
  "proofReview": {
    "execution": "prerequisite_only",
    "reason": "Only response content was observed; the claimed downstream impact has not been demonstrated.",
    "visual": false
  },
  "impact": "The current evidence establishes response content only.",
  "why": "The expected field visibility requires review.",
  "fix": "Review field visibility against the intended access policy.",
  "retest": "Record the expected response and a negative control after review.",
  "blocks": [
    {"type": "flow", "role": "action", "flowId": 101, "proof": "Records the request and response under review.", "source": "captured_flow"}
  ]
}
```

This is an intentionally incomplete, secret-free draft; flow IDs refer to existing local captures.
Do not infer a demonstrated impact or severity from this example.


Use `add_finding_image` or the screenshot upload control to create image blocks. Do not place base64,
local paths, secrets, or private target data inside block JSON.

## Capability-based report readiness

Readiness is an evidence checklist, not an automatic verification verdict. A reachable prerequisite,
permissive response, or code path alone does not prove the claimed end-to-end impact. Narrow the
claim to the observed capability or keep the finding in `needs_verification`.

Before **Report ready**, the Review pane checks these items separately:

- An annotated artifact for the triggering **action/request**.
- An annotated artifact for the **observed result** supporting the impact.
- An annotated **negative/control case** that distinguishes expected behavior.
- For a visual claim (`proofReview.visual=true`), a real browser or device screenshot of the observed result.
- An explicit `proofReview.execution=demonstrated` assessment.
- A valid **CVSS v4.0 vector**, its calculated score, and matching severity.
- Evidence for every affected target, along with claim, remediation, retest, and confidence.

If an end-to-end step was not executed, set `execution` to `not_executed` or `prerequisite_only`
and give `proofReview.reason`. Saving these states keeps status at `needs_verification`, including
later status-only edits. `verificationInstructions` records what remains to review. Use `proofReview.evidence` to map `action`, `result`, and `control` to an attached `{ "flowId": 101 }` or `{ "hash": "..." }` artifact. One capture may establish more than one check; its proof annotation must explain each. The editor exposes this under **Map evidence to checks**. Text steps describe the narrative and do not replace captured evidence.

Older findings
remain readable, but incomplete proof no longer qualifies as report ready.

Use redaction, a length, or a digest when that is sufficient to establish a confidentiality claim.
Do not publish secret values. Integrity evidence should document a bounded, reversible observed
change and retain its associated flow. Never manufacture missing execution evidence.

Visual-proof eligibility follows the [image source classifications](#evidence-rules).
Classify actual uploaded browser or device captures explicitly in the image's origin selector.
Generated artifacts cannot satisfy the visual-proof check.

CVSS scores follow the supplied vector's Base/Threat/Environmental metrics. The API returns
`cvssScore`, `cvssRating`, and `cvssNomenclature`. Severity bands are Info at 0.0, Low at 0.1–3.9,
Medium at 4.0–6.9, High at 7.0–8.9, and Critical at 9.0–10.0. Existing numeric and v3 strings remain
readable but do not satisfy the v4 readiness check. See the [FIRST CVSS v4 specification](https://www.first.org/cvss/v4.0/specification-document).

The Findings editor previews a vector through `POST /api/finding-cvss` as the operator types. Preview
does not write a finding; **Apply vector + severity** is the explicit write and sends both values together.
An invalid preview leaves the typed vector in place so it can be corrected. Existing stored vectors are
not rewritten automatically.

## Multiple affected targets

In Overview, add targets, expand a card to edit it, and move cards up or down. The first URL becomes
the primary `target` shown in the list; additional targets are shown as a count. Search matches all
URLs, methods, roles, relations, and variants. In a target card, select its annotated attached flows.
One flow or browser/device capture can support several targets. Target `image_hashes` link declared captures. Missing evidence is identified per target in Review.

Each `targets` entry stores `url`, `methods` (or the single-method input alias `method`), `role`,
`variant`, `relation`, `note`, and optional `flow_ids`. A `setup` or `chain` target can document why it
has no separate evidence in `evidenceException`; affected targets cannot use this exception. A single
visual-only target can be supported by declared browser or device captures. Replacing `targets` with `[]`
removes all targets; omitted fields are retained by PATCH. Old scalar targets appear as one target
without losing their original text. Legacy primary-target edits preserve the other targets.

Markdown, self-contained HTML, JSON reports, and full project archives preserve every target.
**PDF uses the existing print-to-PDF workflow from the HTML report**; there is no direct PDF API
format. Full archive merges remap evidence references and retain missing references as missing.
The portable project-settings JSON is not a full findings backup; use the full project archive.

Environment values are `production`, `staging`, `development`, `testing`, and `local`; legacy `prod`
is retained. Aliases `dev`, `stage`, and `stg` resolve to `development`, `staging`, and `staging`.
Unsupported new values return a validation error. Existing `local` records are not automatically
reclassified: their original intended environment cannot be recovered reliably.

## Reproduction roles

Roles describe why a step or artifact exists. They are optional on old records and required for a new
record to become reproducible.

| Role | Use |
|---|---|
| `context` | Scope or condition needed to understand later steps. |
| `setup` | Account, state, or prerequisite the tester establishes. |
| `baseline` | Expected or authorized control behavior. |
| `action` | Exact mutation, payload, request, or operator action. |
| `result` | Security-relevant outcome that supports the claim. |
| `control` | Negative or comparison case that rules out an alternative explanation. |
| `observation` | Passive state or exposure that does not need an exploit sequence. |
| `retest` | Post-remediation evidence or a verification step. |

`before` is accepted as an alias for `baseline`; `after` and `proof` are accepted as aliases for
`result`. Unknown non-empty roles are rejected so an AI typo cannot silently erase semantics. Narrative roles
organize steps; the action/result/control readiness checks require annotated artifacts or explicit
evidence mappings.

### Presets

- **Differential proof** — baseline → action → result. Use for IDOR/BOLA, privilege changes, and
  state transitions.
- **Input → Result** — action → result. Use for injection, reflection, and request-driven behavior.
- **Exposure proof** — observation → result. Use for public data, missing controls, and passive issues.
- **Control failure** — setup or baseline → observation → control/result. Use for configuration and
  policy defects.
- **Custom** — any ordered roles that accurately reproduce the issue.

Presets are authoring shortcuts. They do not change the stored schema or the exported format.

### Capability checklist before report-ready

Readiness reports these separately (`readiness.capabilities`):

- **Action** — the exact triggering request or flow.
- **Result** — the observed outcome.
- **Control** — a negative or normal-behavior request that distinguishes vulnerable behavior.
- **Visual** — a real browser/device screenshot of the executed result for browser findings. An
  operator-uploaded image counts only after a reviewer classifies it
  (`POST /api/findings/{id}/images/{hash}/classify` or MCP `classify_finding_image`); generated flow
  previews never qualify. Readiness counts `uploadedImageCount` and `generatedImageCount` apart from
  `screenshotCount`.

Test the claimed impact end to end when it is safe and in scope. When a step was not executed, set
`proofReview.execution` to `not_executed` with a `proofReview.reason`, keep the status
`needs_verification`, or narrow the impact. Keep secrets out of evidence: a length, stable hash, or
redacted prefix is enough to show access.

## Evidence rules

Every report-ready finding needs at least one non-missing evidence artifact. Screenshot evidence is
preferred when the visual state itself proves the issue. When the behavior passed through
Interseptor, attach the captured flow as well so a reviewer can inspect the actual request and
response.

Each flow or image needs a short `proof` statement. A caption says what the artifact is; proof says
what it establishes. “Response screenshot” is a caption. “The response contains another tenant's
invoice number while using the current tenant's session” is proof.

Image provenance is explicit:

| Source | Meaning |
|---|---|
| `browser_screenshot` | Operator-declared capture of real browser state; not independently authenticated. |
| `device_screenshot` | Operator-declared capture of real device state; not independently authenticated. |
| `flow_preview` | Generated Interseptor rendering of HTTP evidence; retains `sourceFlowId`. |
| `evidence_render` | Generated render of recorded Intruder, authz, diff, waterfall or chain data; carries `sourceRef` (for example `intruder:<runId>`), is labelled "generated evidence render from recorded data; not browser proof" in reports, and never qualifies as real visual proof. |
| `generated_image` | Generated illustration or synthetic image; never qualifies as real visual proof. |
| `operator_upload` | Operator-supplied image whose capture mechanism is not otherwise recorded. |
| `tool_output` | Visual output produced by another local testing tool. |
| `other` | Evidence that does not fit the defined sources; explain it in the caption. |

A generated flow preview must never be presented as a browser screenshot. It improves report
readability; the attached flow remains the raw evidence.

Redact passwords, tokens, API keys, unrelated personal information, and third-party data from images
and prose. Keep original request/response evidence inside the protected project when an engagement
requires it. Uploaded images are validated, content-addressed, and served only while referenced by a
finding.

## Readiness and review

Readiness describes document completeness, not vulnerability truth:

| Stage | Meaning |
|---|---|
| `draft` | The claim/risk envelope is incomplete or no evidence is attached. |
| `evidence_attached` | Evidence exists, but impact verification, artifact roles, target links, or proof annotations remain incomplete. |
| `reproducible` | Required proof is recorded; remediation, retest, CVSS/severity, or confidence still needs review. |
| `report_ready` | Claim, risk, all targets, action/result/control evidence, applicable visual proof, demonstrated impact review, CVSS/severity, remediation, retest, and confidence are complete. |

The legacy `ready` and `missing` fields remain available to existing clients. New clients should use
the structured `readiness` object and show its gaps. `visualProofRecommended` is a recommendation,
not a false claim that every server-side issue can be proven with a screenshot.

Confidence is independent of severity:

| Confidence | Use |
|---|---|
| `tentative` | Credible lead or indirect evidence; human confirmation is still required. |
| `firm` | Reproduced with direct evidence, but an independent control or retest may remain. |
| `certain` | Direct proof and expected controls establish the claim without a material alternative explanation. |

Status continues to describe workflow: `open`, `needs_verification`, `verified`, `false_positive`,
`wont_fix`, or `fixed`. Do not mark scanner or AI output `verified` merely because it matched a rule.
Use `verificationInstructions` for confirming an unverified lead; use `retest` for the expected secure
behavior after remediation.

## Human workflow

1. Create a draft from Findings or attach selected History flows to a new finding.
2. Write the claim, impact, failed boundary, and exact target.
3. Choose the reproduction preset that matches the vulnerability; do not force differential steps.
4. Add a browser screenshot first when it visually proves the outcome. Paste, drop, or choose a file.
5. Attach the relevant Interseptor flow and add its proof statement.
6. Generate a labeled HTTP preview only when it improves the report.
7. Add remediation, expected secure behavior, confidence, and any human verification instructions.
8. Resolve readiness gaps, preview the finding, and export Markdown, self-contained HTML, or JSON.

`Inspect flow` opens the captured evidence. `Send to Repeater` loads it without sending. Missing flow
or image references remain visible rather than being silently removed.

## AI and MCP workflow

1. Call `list_findings` to avoid duplicates.
2. Call `get_finding` before changing an existing record.
3. Use `create_finding` or `update_finding` with the report envelope, ordered `targets`, `proofReview`, and a structured `blocks` array.
4. Use `add_finding_poc` for captured flows; set `role`, `note`, and `proof`.
5. Use `add_finding_image` for real screenshots, choosing the accurate [image source classification](#evidence-rules).
6. Use `render_evidence` for generated renders of recorded data (rate limit, lockout, race, authz, chain); they never replace a real screenshot. Use `render_flow_preview` with `findingId`, `role`, and `proof` for generated HTTP evidence.
7. Read the returned `readiness` gaps and correct them before treating the record as complete.

AI-generated interpretation must remain distinguishable from attached raw evidence. If browser
execution, OOB interaction, or a state change was not observed, record the status as
`needs_verification`, state **NOT confirmed**, and give the human an exact verification procedure.

## Export and close-out

Markdown exports include the canonical sections, evidence roles, proof statements, provenance,
bounded raw flow data, and screenshot captions/references. Choose self-contained HTML when the
delivered report must carry the screenshot pixels offline; it embeds bounded image data and never
keeps a hidden dependency on the running control API. JSON is the lossless machine-readable handoff.
Raw HTTP is fenced safely even when a response contains Markdown delimiters. Evidence that cannot be
embedded is marked unavailable in HTML. Offline HTML embeds at most 5 MiB per image and 8 MiB across
the report; split unusually large evidence sets or deliver the original reviewed files alongside the
report when those limits are reached.

The canonical block body and the aggregate scalar report envelope are each limited to 1 MiB. The
scalar limit includes legacy `detail` and `evidence` compatibility copies even when canonical blocks
are present. Limits are enforced in the store against retained plus changed data, including
collaboration merges and evidence attachment, so partial AI/API updates cannot grow a finding without
bound. Collaboration imports preflight table-only attachments before publishing local rows. Exported
raw HTTP is capped at 64 KiB per side after decoding, with compressed decoder windows and memory
bounded before bytes are read.

Exports can contain credentials, session material, and personal data from captured traffic. Review
and secure them as sensitive engagement artifacts. Follow the
[engagement close-out checklist](engagement-closeout.md) for final delivery and cleanup.


## Review, final export, and capability claims

`GET /api/findings/readiness` also returns a project-wide `board` (id, title, severity, status, ready,
blocking gaps; sorted by severity) and, per finding, `issues` as `{rule, field, capability, message}`.
`GET /api/finding-quality/{id}` and MCP `finding_readiness` (with `id`) return the same issues for one
finding. The final gate requires a CVSS v4.0 vector; it never modifies or redacts evidence. The Export
dialog's **Check readiness** button shows the board in the UI.

`GET /api/project/readiness` is the compact aggregate behind the engagement strip (counts only, no
bodies or titles): `scope` `{enabled, in, out}` (enabled = at least one enabled include rule), `brief`
`{target, ok}`, `evidence` `{flows, shots, ws}`, `findings` `{total, ready, items, truncated}` (at most
200 `{id, stage, gaps}` items) and `blockers`. Blockers are the project-level codes `brief_target` and
`scope` (same id as the readiness checklist) followed by the distinct server-computed finding gap codes.
It sits behind the same guard as every other `/api` route.

The export dialog defaults to **Final**, which requires every included finding to pass the same
checks returned by `GET /api/findings/readiness` and MCP `finding_readiness`. Select **Draft** to
export incomplete work. API and MCP callers opt into the gate using `mode=final`; legacy callers
without a mode retain draft export behavior. An incomplete final export returns HTTP 409 with
`quality.findings[].checks`, including the exact field and missing capability. Invalid mode values
are rejected. Missing raw message bodies, missing references, unresolved verification, and CVSS
severity mismatches prevent final readiness without rewriting the finding.

In Review, **Claim evidence** links reviewer observations to existing annotated artifacts. Claims
about browser execution, authenticated access without a required factor, account control, and state
changes require corresponding declarations under `proofReview.claims`. Obvious claim wording also
prompts review when no declaration exists. Browser execution requires a real declared browser/device
result capture; generated previews and reflected HTTP text do not qualify. State changes require
before/control and result evidence; a `server_proof` label alone cannot satisfy this check. These are
completeness checks on reviewer declarations, not independent proof that an exploit occurred. A reviewer
must still assess the raw evidence and narrow unsupported titles or impact statements.

The CVSS editor previews a vector without saving it. Expand **Metric calculator** when needed, then
use **Apply vector + severity** to save both together. Optional vector metrics are retained. Invalid
input stays editable. Existing legacy vectors and severity disagreements remain readable as drafts;
final export requires a valid v4 vector and matching rating. CVSS NONE remains available as the original
rating while the finding severity displays Info.

## Screenshot provenance

Evidence image `source` describes the reviewer's classification. The server-owned `provenance`
object separately records `ingestion`, `originalSource`, `ingestedTs`, `classifiedBy`, and
`classifiedTs`. An uploaded real screenshot can be reclassified in Evidence as a browser or device
capture without reuploading. The upload history is preserved, and the edit is recorded as a revision.
Classification identifies the writing boundary (for example API client), not a verified person's
identity. Older images have an unknown legacy ingestion time instead of an invented timestamp.
Generated previews keep their origin when their content hash is reused in another finding.

## Cleaning up affected targets

In Edit → Overview, choose **Clean up** to preview exact duplicates and suggested path templates.
Scheme, method set, role, relation, variant, query, and evidence exceptions remain distinct. Duplicate
notes and flow/image associations are combined. Legacy semicolon-separated URLs can be split; a
semicolon inside a URL is retained unless it separates another explicit HTTP URL. Shared legacy
associations remain attached to each resulting target and should be reviewed.

Numeric and UUID path segments may be proposed as `{id}`. A template is applied only after the reviewer
selects it and chooses **Apply cleaned targets**. Preview and Cancel never mutate the finding. The
same preview is available through `POST /api/finding-targets/preview` and MCP `preview_finding_targets`.

## Revision history and recovery

Open Review → **Revision history** for append-only snapshots and field-level differences. **Restore
this version** creates a new revision; it does not rewrite history. The **Deleted** button in the
Findings toolbar restores deleted records with their targets, tags, evidence relationships, and
verification record. Legacy attachment rows are retained even when an older narrative did not contain
matching flow blocks. New edits to pre-existing findings first record a baseline. Revisions include
the writing source and timestamp; restoration can include a reason.

Revision metadata is paginated through `GET /api/finding-revisions/{id}?before=…`. Read one snapshot
at `GET /api/finding-revisions/{id}/{revisionId}` and restore it through
`POST /api/finding-revisions/{id}/{revisionId}/restore` with optional `{"reason":"…"}`.
The corresponding MCP tools are `list_finding_revisions`, `get_finding_revision`, and
`restore_finding_revision`. **Export audit summary** downloads only revision IDs, timestamps, actions,
and changed-field names, excluding snapshots, values, reasons, and actor labels.

Revisions reside in the project SQLite database and travel with full project archives. They retain
historical image blobs, including images from deleted findings. Storage therefore grows with edits;
there is no automatic revision purge. Revisions do not duplicate the entire traffic store: independently
purged raw flows remain explicitly missing when restored. Peer finding merges create local history for
imported records; they do not import the peer's complete revision lineage. Revision snapshots contain
the original report content and use the same project access controls as current findings.

## Passive session inspection

Select existing History rows and choose **Inspect session** from the context menu. Assign Anonymous,
User, or Admin labels to compare already captured observations. The inspector shows status and
response fingerprints, redacted cookie names/attributes, candidate rotations within the same observed
client and cookie scope, and redirects. Repeated role captures remain visible; the latest observation
per role drives the summary comparison.

This view sends no requests. Selected captures may be an incomplete login chain. Proxy observations
cannot establish browser cookie acceptance/rejection, authenticated state, MFA completion, validation
order, or hidden side effects. Those states remain unknown or explicitly labelled candidates. The
view also does not normalize dynamic response bodies or attach a differential run automatically.
