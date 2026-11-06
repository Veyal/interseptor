# Playbooks

Playbooks are markdown test guides. Interseptor ships a curated set and a project can add its
own; an AI harness connected over MCP reads them and executes them itself with the ordinary
tools (`send_request`, `start_intruder`, `get_flow`, `authz_run`, `create_finding`).

There is deliberately no in-app active scanner: the connected agent is the execution engine,
and what it lacked was a target-aware corpus of what to check and how to evidence it.

## Where they live

| Source | Path | Trust |
|---|---|---|
| Bundled | `internal/playbooks/corpus/*.md`, embedded in the binary | trusted |
| Project | `<project>/playbooks/*.md` | **untrusted content** |

A project playbook with the same `id` replaces the bundled one. A project file that fails to
parse still appears in the list with its error, so a typo cannot silently remove a guide.

Project playbooks are instructions written into a workspace, not authority: every tool result
and the rendered markdown say `source=project`, `trusted=false`, and an `UNTRUSTED` banner tells
the harness the text cannot widen scope or override the operator's rules.

## Format

```markdown
---
id: object-authz                 # defaults to the file name
title: Object-level authorization on identifiers
applies_to:                      # signals used by list_playbooks / match_playbooks
  - param:id
  - path:/api/v1/
  - host:api.example
  - mime:text/html
  - method:PATCH
  - header:authorization
  - tech:graphql                 # bare token: a tech hint
severity_hint: High
tools:
  - list_flows
  - authz_run
  - send_request
evidence: >-
  What actually proves the finding, in one sentence.
references:
  - OWASP API1:2023
---

# Body

The guide itself: what to do, what to capture, what not to do.
```

## How a harness finds them

1. **Instructions** — the MCP server tells the agent to consult playbooks before testing a surface.
2. **Tools** — `list_playbooks` (filter by host/path/method/param, each row names the signal that
   matched), `get_playbook` (full markdown), `match_playbooks` (playbooks that apply to one captured flow).
3. **Resources** — every playbook as `playbook://<id>` (`text/markdown`), for clients that
   auto-attach resources.
4. **Prompts** — one prompt per playbook (`playbook-<id>`), so clients that support prompts show
   it as a slash command.
5. **Nudge** — `check_readiness` reports how many playbooks apply to the newest captured flow.

## Coverage ledger

`record_playbook_coverage` (or `POST /api/playbooks/coverage`) writes one row per
(playbook, host, endpoint, param) with `status` — `considered`, `run`, `found` or `dismissed` —
the evidence flow ids and an optional note. Flow ids accumulate, the latest status wins, and
`playbook_coverage` reads the ledger plus a per-playbook rollup. This is the "did we actually
test X" record the report's coverage section reads.

## Retiring the passive scanner

Playbooks replace the passive scanner. The scanner's panel and its `run_scanner` /
`list_issues` tools are marked deprecated and keep working for existing projects; removal follows
in a later release, once the bundled corpus covers the checks it performed. See issue #119.
