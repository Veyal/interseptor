---
name: finding-format
description: Enforce structured, impact-first finding markdown for MCP create_finding / update_finding.
---

# Finding format (MCP)

Agents must not file walls of text. Format is enforced in `internal/mcp/finding_format.go`.

## Point-first pillars

| Field | Create | Report-ready |
|---|---|---|
| `title` | required | required |
| `severity` / `status` | defaults OK | set |
| `impact` | blank OK | required |
| `why` | blank OK | required |
| `target` | blank OK | required |
| PoC body (flows/images) | blank OK | ≥1 flow **or** image; Critical/High: ≥2 flows (Before + After) |

Optional: `cwe`, `environment` (`prod`|`staging`|`local`), `fix`/remediation, `cvss`.

Stub create (title only) is allowed. Expand pillars before treating the finding as done.

## PoC / Evidence (body timeline)

Keep `body` as an ordered exploit chain — not an essay:

1. Short step notes (`text`) — label **Before → Action → After** (or IDOR: our account → other id → cross-access)
2. Attach flows (`add_finding_poc`) and/or screenshots (`render_flow_preview` / `add_finding_image`)
3. Never claim success without an After (or cross-access) artifact — flow and/or image
4. Captions/notes: one sentence on what changed and why it proves Impact

Do **not** dump long `## Summary` / `## Steps` / `## Evidence` walls into body text. Put Impact/Why in their fields.

`needs_verification` → set `verificationInstructions`; say **"NOT confirmed"** when XSS/JS was not proven.

## Enforcement

| Case | Behavior |
|---|---|
| ≥180 chars narrative, no headings, empty impact+why | **Reject** tool call |
| Substantial write missing Impact/Why/Target/PoC; High without enough flows; bare credentials; needs_verification w/o instructions | **FORMAT WARNING** appended to success |
| Title-only stub | Allowed |

Human UI creates are not gated — MCP only. UI shows Draft vs Ready from the same completeness rules.

The human-facing writing and review standard lives in `docs/findings-and-reporting.md` and the
Findings **Writing guide** modal. Keep those two surfaces aligned with this enforcement contract when
fields, readiness rules, evidence actions, or status meanings change.

## Do not

- Put base64/`path` image data in body JSON — use `add_finding_image`
- Paste raw HTTP into `evidence`/`detail` when a flow can be attached
- File freeform essay findings — use structured fields + PoC timeline
