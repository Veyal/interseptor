---
name: finding-format
description: Enforce structured, impact-first finding markdown for MCP create_finding / update_finding.
---

# Finding format (MCP)

Agents must not file walls of text. Format is enforced in `internal/mcp/finding_format.go`.

## Required sections (in text blocks)

1. `## Summary` — first sentence = **impact** (CIA / what attacker gains), not mechanism
2. `## Evidence` — proof details; credentials in a **table** or **bold** list
3. `## Impact` — concrete confidentiality/integrity/availability consequence
4. PoC via `add_finding_poc` / body `flow` blocks (required for Critical/High)
5. Say **"NOT confirmed"** when JS/XSS execution was not proven
6. `needs_verification` → set `verificationInstructions` + optional `## Needs Verification`

Interleave: text → flow → text → image → flow (notebook, not a dump).

## Enforcement

| Case | Behavior |
|---|---|
| ≥180 chars narrative, no `##` heading | **Reject** tool call |
| Missing Summary/Evidence/Impact, High without flow, bare credentials, needs_verification w/o instructions | **FORMAT WARNING** appended to success |
| Short opening detail ("IDOR on /x — PoC next") | Allowed |

Human UI creates are not gated — MCP only.

## Do not

- Put base64/`path` image data in body JSON — use `add_finding_image`
- Paste raw HTTP into `evidence`/`detail` when a flow can be attached
