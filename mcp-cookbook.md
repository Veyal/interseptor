---
layout: default
title: MCP cookbook
classification: current
source: docs/product/mcp-cookbook.md
---
<p class="eyebrow">CURRENT</p>
# MCP Cookbook, recipes for external agents

*Connect any MCP-capable external agent with `interseptor mcp` or
`POST http://127.0.0.1:9966/mcp`. Interseptor provides deterministic tools and evidence. Your agent
provides model reasoning and sequencing.*

## Recipe 1 — Map an API from captured traffic

**Goal:** Triage what landed in History and pick endpoints worth attacking.

```
1. list_flows with search set to the target host
2. analyze_flow on the most interesting ids (POST/PUT, auth headers, 4xx/5xx)
3. get_flow for bodies you need to read
4. flow_as_curl on anything you want to replay manually
```

**Tip:** Add an include-scope rule (`add_scope_rule`) first so `list_flows` isn't noisy.

## Recipe 2 — Content discovery through the proxy

**Goal:** Find hidden paths, with every hit landing in History for triage.

Interseptor has no built-in forced-browser (the old `start_discovery` /
`discovery_state` / `suggest_discovery_paths` tools were removed — see
CHANGELOG). Run a real tool instead, pointed **through** the Interseptor
proxy, so every request it fires is captured like any other traffic:

```
1. list_scope — confirm the target is in scope
2. Run a forced-browse tool through the proxy, e.g.:
   feroxbuster -u https://target/ --proxy http://127.0.0.1:8080 -k
   (or gobuster / ffuf configured with the same proxy)
3. list_flows with search set to the target host to see what landed
4. host_stats for a per-host summary of what was hit
5. send_request on interesting hits; run_scanner for passive follow-up
```

**Human takeover:** Watch Proxy History live — hits appear as normal
captured flows, no separate Discover tab.

## Recipe 3 — Triage scanner findings and fuzz

**Goal:** Turn passive hits into confirmed bugs.

```
1. run_scanner
2. list_issues — read severity/title/evidence
3. scan_report for a Markdown summary to paste into notes (append_notes)
4. For a reflected-param finding: get_flow → start_intruder with § markers
5. set_session if sends need auth; run_login_macro after a 401
```

**Safety:** `active_scan` sends real payloads — pass `arm=true` once per session and only on authorized targets.

## Recipe 4 — Close out findings (engagement end)

**Goal:** Leave a report-ready project, not a History pile.

```
1. list_finding_tags / list_findings — triage status / severity / ready / tags
2. For each stub: update_finding with Impact / Why / Target + tags (cms|website|app|api)
3. get_flow + add_finding_poc for proof flows (and screenshots if needed)
4. Mark uncertain items needs_verification with concrete check steps
5. Export via UI (Group by tag) or GET /api/findings/report?groupBy=tag&omitTags=out-of-scope
6. Optional: export_full_project for a portable archive
```

**Human checklist:** [engagement-closeout.md]({{ "/engagement-closeout/" | relative_url }})

## Recipe 5 — External agent active testing

**Goal:** Let an external agent sequence deterministic scans and human-reviewed evidence.

```
1. list_scope — confirm include rules before any active request
2. check_readiness — fix blockers (auth, traffic, OOB when needed)
3. run active_scan with arm=true on authorized targets
4. Review Activity and History while requests run
5. Reproduce important candidates with send_request or Repeater
6. create_finding and add_finding_poc only after evidence review
```

**Safety:** Interseptor doesn't decide what to test. The external agent must follow scope and
authorization constraints, while Interseptor records requests and results.

## Recipe 6 — Custom checks and rule packs

**Goal:** Encode a finding as a reusable check, or install an official pack.

```
1. list_checks / list_active_checks — see what's loaded
2. Author offline: `interseptor check new` → validate → test
3. save_check / save_active_check when ready (or drop into Checks UI)
4. list_packs / pack_info — see installed packs (install is human-gated)
5. Human: Scanner → Checks → Install official pack, or
   `interseptor rules install pack.tar.gz`
```

## Recipe 7 — Intruder from external payload lists

**Goal:** Fuzz one injection point with AI-suggested payloads, then file a finding.

```
1. get_flow on the target exchange
2. Prepare payloads in your external agent, based on the captured flow
3. start_intruder using positions and payloads
4. After the run: inspect flagged / interesting results in History
5. create_finding + add_finding_poc with the best attempt's flowId
```

