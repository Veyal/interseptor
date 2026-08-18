---
layout: default
title: Engagement close-out
classification: current
source: docs/engagement-closeout.md
---
<p class="eyebrow">CURRENT</p>
# End an engagement — close-out checklist

Use this when you are wrapping a pentest / bug-bounty session in Interseptor.
The goal is a **report-ready project**, not just a pile of History rows.

## 1. Scope & capture

- [ ] Target scope include rules cover every host you tested
- [ ] Out-of-scope noise is excluded (or filtered via Views)
- [ ] Session / login macro still refreshes auth if you need last-minute PoCs

## 2. Triage → Findings

- [ ] Run **Scanner** (passive) and triage hits worth promoting
- [ ] Review scanner and agent evidence, then file findings manually or through MCP — prefer point-first findings
  (Impact / Why / Target + PoC timeline)
- [ ] Tag findings by deliverable scope (`cms` / `website` / `app` / `api`; use
  `out-of-scope` for adjacent evidence you want to keep out of the client pack)
- [ ] Attach **PoC flows** (`add_finding_poc` / UI) and screenshots where needed
- [ ] Mark uncertain items `needs_verification` with concrete check steps
- [ ] Intruder: filter **Interesting** → **→ Finding** to attach flagged attempts

## 3. Active work and external orchestration

- [ ] Review external-agent activity and verification results in History
- [ ] Reproduce important candidates with `send_request` or Repeater
- [ ] Attach evidence flows and record concrete verification steps
- [ ] Confirm Critical/High with a human read of the PoC

## 4. Export & handoff

- [ ] Export **Findings report** (Markdown / HTML / JSON) with PoC bodies as needed;
  enable **Group by tag** (omits `out-of-scope` by default) for multi-scope write-ups
- [ ] Export **full project** zip if the client needs a portable archive
- [ ] Copy deep links (`/#finding-N`, `/#flow-N`) into notes / ticket system
- [ ] Install any custom checks you want to reuse as a **rule pack** for next time

## 5. Hygiene

- [ ] Retention policy set if the project will sit idle (`Settings → Project & data`)
- [ ] API keys still valid for remote/Tailscale follow-up
- [ ] Clear or archive the project when the engagement is done

For agent-driven close-out, see [MCP cookbook]({{ "/mcp-cookbook/" | relative_url }}) recipe **Close out findings**.

