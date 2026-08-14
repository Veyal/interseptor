# Findings and reporting

Findings are curated, durable vulnerability records. Scanner issues are leads; promote only validated
issues into Findings, attach the minimum useful evidence, and write enough context for an independent
pentester to reproduce the result.

## Recommended workflow

1. Capture a clean baseline request and the security-relevant response.
2. Reproduce the behavior in Repeater or the appropriate testing tool.
3. Create a finding from selected History flows, or create a draft in **Findings**.
4. Fill Impact, Why, Target, classification, and the ordered PoC chain.
5. Attach before/action/after flows and screenshots with short proof annotations.
6. Mark the status accurately, export a draft, and have another operator retest it.

The readiness banner is a completeness check, not proof that a vulnerability is real. High and
Critical findings require before-and-after evidence because the security boundary must be visible.

## Technical finding template

### Title

Name the weakness, affected action, and asset. Use a specific outcome:

- Good: `Broken object authorization exposes another customer's invoice`
- Weak: `IDOR issue`

Avoid severity, speculation, and duplicate endpoint details in the title.

### Impact

State what a realistic attacker gains, required privileges or position, the affected data or action,
and meaningful scale. Do not merely repeat the payload or HTTP status.

Example: “A basic account can download invoices belonging to other organizations, exposing customer
names, addresses, and billing history.”

### Why it is a finding

Identify the failed security boundary. Tie the observation to authorization, authentication, trust,
data validation, state transition, or isolation. This lets engineering distinguish a vulnerability
from intended product behavior.

### Target and classification

Record the exact environment and affected route or component without embedding live credentials.
Add CWE, CVSS score/vector when required by the engagement, and stable scope tags such as `api`,
`website`, `mobile`, or a report section name. Tags are also used for filtering and grouped exports.

### Reproduction

Write a **Before → Action → After** chain. Each step should contain one operator action and one
observable result:

1. State prerequisites: account role, object ownership, feature flags, and starting state.
2. Attach the baseline flow and annotate the identity and expected access.
3. Describe the exact mutation: parameter, header, body field, method, or sequence change.
4. Attach the exploit flow and identify the response fragment or state change that proves impact.
5. Include a negative/control request when it distinguishes the vulnerability from normal behavior.

Use “send request with `account_id=42`” rather than “change the ID.” State whether IDs are examples
and avoid copying real customer data into prose.

### Evidence

Attach only the flows and screenshots needed to establish the claim. Give every flow a proof-oriented
annotation, for example “low-privilege identity receives another tenant's record.” A status code alone
is rarely sufficient; point to the returned field, authorization difference, OOB interaction, or
confirmed state change.

Redact session tokens, passwords, API keys, unrelated personal information, and third-party data from
screenshots and exported prose. Keep original request/response evidence only inside the protected
project when the engagement requires it.

Use **Inspect request** to review an attached flow. **Send to Repeater** loads the complete captured
request into an endpoint-matched Repeater tab; it does not send the request until you press Send.

### Remediation and retest

Recommend the control at the failed trust boundary, not only a payload blacklist. Include enough
specificity to be actionable, but allow engineering to choose the implementation. Describe the secure
expected result and a negative test for retesting.

For object authorization, for example: enforce ownership/tenant authorization server-side on every
object lookup; retest with two unrelated accounts and confirm the cross-tenant request returns a
denial without disclosing whether the object exists.

## Status lifecycle

| Status | Use when |
|---|---|
| `open` | Reproduced and actionable, awaiting remediation or triage. |
| `needs_verification` | A scanner or agent produced a credible lead that a human must confirm. |
| `verified` | A human or accepted proof workflow reproduced the vulnerability. |
| `false_positive` | The behavior is not vulnerable after investigation. Preserve the rationale. |
| `wont_fix` | Accepted risk or out-of-scope remediation decision. Record the decision context. |
| `fixed` | Remediation was retested and the secure expected result was observed. |

Do not use `verified` as a synonym for “scanner matched.” Do not delete false positives when their
rationale is useful to avoid repeat work.

## Readability checklist

Before export, confirm that:

- a pentester unfamiliar with the target can reproduce the result;
- a developer can identify the failed control and affected component;
- the evidence proves the stated impact rather than only showing an unusual response;
- severity is consistent with prerequisites, scope, and realistic consequence;
- examples contain no live secrets or unrelated private data;
- a retester knows the expected secure behavior.

## Export and close-out

Findings export supports Markdown, HTML, JSON, and PDF, status filters, body inclusion, and grouping by
tag. Exported PoC flows can include reconstructed request/response bodies, so handle reports as
sensitive engagement material. Review the output rather than treating generation as final editorial
approval.

Follow the [engagement close-out](engagement-closeout.md) guide for final exports, project archives,
credential cleanup, and CA removal.
