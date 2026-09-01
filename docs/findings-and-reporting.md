# Findings and reporting

Findings are durable vulnerability records, not scanner alerts or prose notes. One canonical record is
used by the Findings UI, REST API, MCP tools, JSON export, and human-readable reports. A draft may be
incomplete; a report-ready finding must connect every material claim to reproducible evidence.

## Why the format changed

`Before → Action → After` is useful for differential authorization, privilege, and state-change
findings. It is not a universal reporting structure. It makes passive misconfiguration, exposure,
SSRF/OOB, and browser-execution findings harder to describe and encourages agents to manufacture
irrelevant “before” steps.

Interseptor therefore uses a universal evidence-first envelope and optional reproduction presets:

1. **Claim** — title and concise summary.
2. **Risk** — attacker impact and the failed security boundary or root cause.
3. **Affected target** — exact component, environment, CWE, CVSS, and scope tags.
4. **Reproduction** — ordered steps with explicit semantic roles.
5. **Evidence** — captured flows and images, each tied to the claim it proves.
6. **Fix and retest** — remediation, expected secure behavior, and a negative test.
7. **Review** — status, confidence, source, and verification state.

This follows OWASP guidance that a finding must be understandable, reproducible, actionable, and
supported by images or other artifacts. OWASP's emerging autonomous-testing guidance additionally
calls for raw evidence, provenance, and a clear separation between machine interpretation and the
underlying proof. See the [OWASP WSTG reporting structure](https://owasp.org/www-project-web-security-testing-guide/latest/5-Reporting/01-Reporting_Structure)
and [OWASP APTS evidence guidance](https://owasp.org/APTS/standard/8_Reporting/Implementation_Guide.html).

## Canonical finding envelope

The canonical machine shape is the normal finding JSON returned by `GET /api/findings/{id}`. New
clients should send a `blocks` array. The legacy `body` JSON string remains accepted for backward
compatibility, but a request must not send both.

```json
{
  "title": "Broken object authorization exposes another account's invoice",
  "summary": "A signed-in user can retrieve an invoice owned by another organization.",
  "severity": "High",
  "status": "verified",
  "confidence": "certain",
  "source": "human",
  "target": "GET https://api.example.com/invoices/{invoice_id}",
  "environment": "staging",
  "cwe": "CWE-639",
  "cvss": "CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N",
  "impact": "A basic account can read billing data belonging to another organization.",
  "why": "The API resolves the object without enforcing tenant ownership.",
  "fix": "Authorize every invoice lookup against the authenticated tenant.",
  "retest": "Repeat with two unrelated accounts and confirm a uniform denial without object metadata.",
  "blocks": [
    {
      "type": "text",
      "role": "baseline",
      "md": "Sign in as the example account and request an invoice it owns."
    },
    {
      "type": "flow",
      "role": "baseline",
      "flowId": 101,
      "note": "Authorized control request",
      "proof": "Establishes the response shape for an invoice owned by the current account.",
      "source": "captured_flow"
    },
    {
      "type": "text",
      "role": "action",
      "md": "Replace the invoice identifier with an example identifier owned by another organization."
    },
    {
      "type": "flow",
      "role": "result",
      "flowId": 102,
      "note": "Cross-tenant response",
      "proof": "The current session receives the other organization's invoice fields.",
      "source": "captured_flow"
    },
    {
      "type": "image",
      "role": "result",
      "hash": "<content-addressed image hash>",
      "mime": "image/png",
      "caption": "Cross-tenant response highlighted for the report",
      "proof": "Visually identifies the foreign invoice fields returned to the current session.",
      "source": "flow_preview",
      "sourceFlowId": 102
    }
  ],
  "tags": ["api", "authorization"]
}
```

Use `add_finding_image` or the screenshot upload control to create image blocks. Do not place base64,
local paths, secrets, or private target data inside block JSON.

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
`result`. Unknown non-empty roles are rejected so an AI typo cannot silently erase semantics.

### Presets

- **Differential proof** — baseline → action → result. Use for IDOR/BOLA, privilege changes, and
  state transitions.
- **Input → Result** — action → result. Use for injection, reflection, and request-driven behavior.
- **Exposure proof** — observation → result. Use for public data, missing controls, and passive issues.
- **Control failure** — setup or baseline → observation → control/result. Use for configuration and
  policy defects.
- **Custom** — any ordered roles that accurately reproduce the issue.

Presets are authoring shortcuts. They do not change the stored schema or the exported format.

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
| `browser_screenshot` | Real browser or device state captured by the operator or an approved browser tool. |
| `flow_preview` | Generated Interseptor rendering of HTTP evidence; retains `sourceFlowId`. |
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
| `evidence_attached` | Evidence exists, but reproduction roles or proof annotations are incomplete. |
| `reproducible` | Another tester can follow the steps and understand the proof; fix, retest, or confidence is still incomplete. |
| `report_ready` | Claim, risk, target, reproduction, annotated evidence, remediation, retest, and confidence are complete. |

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
3. Use `create_finding` or `update_finding` with the scalar envelope and a structured `blocks` array.
4. Use `add_finding_poc` for captured flows; set `role`, `note`, and `proof`.
5. Use `add_finding_image` for real screenshots, with `source=browser_screenshot` when accurate.
6. Use `render_flow_preview` with `findingId`, `role`, and `proof` for generated HTTP evidence.
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
