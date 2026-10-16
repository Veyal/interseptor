# End an engagement — close-out checklist

Use this when you are wrapping a pentest / bug-bounty session in Interseptor.
The goal is a **report-ready project**, not just a pile of History rows.

## 1. Scope & capture

- [ ] Target scope include rules cover every host you tested
- [ ] Out-of-scope noise is excluded (or filtered via Views)
- [ ] Session / login macro still refreshes auth if you need last-minute PoCs

## 2. Triage → Findings

- [ ] Run **Scanner** (passive) and triage hits worth promoting
- [ ] Review scanner and agent evidence, then file findings manually or through MCP using the shared
  evidence-first envelope: claim/summary, impact, why, target, typed reproduction blocks, fix, retest,
  and confidence
- [ ] Tag findings by deliverable scope (`cms` / `website` / `app` / `api`; use
  `out-of-scope` for adjacent evidence you want to keep out of the client pack)
- [ ] Attach **PoC flows** (`add_finding_poc` / UI) with a role and exact proof statement
- [ ] Attach a real browser/device **screenshot as primary visual evidence** when it visibly proves the issue;
  use `add_finding_image` with `source=browser_screenshot`. Use `render_flow_preview` for a labeled visual
  HTTP record and `render_evidence` for generated renders of recorded Intruder/authz data (`source=evidence_render`; never browser proof), not as a substitute for a real screenshot when one is available
- [ ] Mark uncertain items `needs_verification` with concrete check steps
- [ ] Intruder: filter **Interesting** → **→ Finding** to attach flagged attempts

## 3. Active work and external orchestration

- [ ] Review external-agent activity and verification results in History
- [ ] Reproduce important candidates with `send_request` or Repeater
- [ ] Attach evidence flows and record concrete verification steps
- [ ] Confirm Critical/High with a human read of the PoC

## 4. Retest before you write it up

- [ ] Retest the findings you are claiming (`POST /api/findings/{id}/retest`) and read the
  observation, not a verdict: `unchanged`, `changed` or `inconclusive`. A differing response is not
  proof of remediation, and the retest never moves the finding's status
- [ ] Leave non-idempotent requests skipped unless you mean to re-send them (`allowUnsafeMethods`)
- [ ] Keep the attached `retest` evidence block: it is a new revision, and the original evidence
  stays as it was

## 5. Export & handoff

- [ ] Set **report branding** (`PUT /api/report-branding`) if the deliverable is client-facing:
  report title, company and client names and logos, assessment dates, lead assessor, classification,
  and a cover page. Unset leaves the default Interseptor report unchanged
- [ ] Export **Findings report** (Markdown / self-contained HTML / PDF / JSON / SARIF) with PoC
  bodies as needed; enable **Group by tag** (omits `out-of-scope` by default) for multi-scope
  write-ups
- [ ] Decide on **credential masking** (`?maskHeaders=1`). Off by default, which is right for an
  internal evidence pack; on for a client deliverable, which replaces `Authorization`, `Cookie`,
  `Set-Cookie` and `Proxy-Authorization` values in attached raw evidence with a length and digest.
  It does not touch bodies, URLs or prose — read the secret-lint notice at the top of the HTML
  report for those
- [ ] For PDF, confirm a headless Chrome is available (`INTERSEPTOR_CHROME` to point at one). A
  missing browser is a 503 naming it, never a silent fall back to HTML
- [ ] File into a tracker or platform from `GET /api/finding-export/{id}?format=…`
  (HackerOne / Bugcrowd / Jira / SARIF). Add the identifiers only you hold — a Jira project key, for
  example — before posting
- [ ] Export **full project** zip if the client needs a portable archive
- [ ] Copy deep links (`/#finding-N`, `/#flow-N`) into notes / ticket system
- [ ] Install any custom checks you want to reuse as a **rule pack** for next time

## 6. Hygiene

- [ ] Retention policy set if the project will sit idle (`Settings → Project & data`)
- [ ] API keys still valid for remote/Tailscale follow-up
- [ ] Clear or archive the project when the engagement is done

For agent-driven close-out, see [MCP cookbook](product/mcp-cookbook.md) recipe **Close out findings**.

### Evidence format

Findings use one ordered `blocks` array across the UI, REST API, MCP, and reports. Blocks are text,
captured flows, or images. Give evidence a role (`baseline`, `action`, `result`, `control`, or
`retest`), state exactly what it proves, and preserve provenance (`captured_flow`,
`browser_screenshot`, `flow_preview` plus `sourceFlowId`, or `evidence_render` plus `sourceRef` where applicable). Before→Action→After
is an optional Differential preset for authorization/state comparisons; use a shorter role sequence
when the vulnerability does not require a before/after comparison. See [API & MCP](api-and-mcp.md)
for the complete field and endpoint contract.
