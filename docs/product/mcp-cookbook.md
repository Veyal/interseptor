# MCP Cookbook, recipes for external agents

*Connect any MCP-capable external agent with `interseptor mcp` or
`POST http://127.0.0.1:9966/mcp`. Interseptor provides deterministic tools and evidence. Your agent
provides model reasoning and sequencing.*

*If your client loads every tool schema into every prompt, narrow the surface first:
`INTERSEPTOR_MCP_PROFILE=web-pentest` (or `?profile=web` over HTTP) serves what these recipes use, at
about a third of the full schema size. Recipe 4 wants `reporting`; Recipe 5's device setup wants
`mobile`. `full` stays the default and serves everything. See
[API and MCP](../api-and-mcp.md) for how to select one, and `GET /api/mcp` for what each serves.*

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
5. match_playbooks on interesting flows; inspect and test applicable guides
```

**Human takeover:** Watch Proxy History live — hits appear as normal
captured flows, no separate Discover tab.

## Recipe 3 — Test a captured surface with playbooks

**Goal:** Investigate a lead and record the evidence.

```
1. match_playbooks for a captured flow; get_playbook for the applicable guide
2. get_flow and analyze_flow to inspect the request and response
3. Establish a baseline, then use send_request or start_intruder to test one hypothesis
4. Compare responses with diff_flows and record evidence for any finding
5. record_playbook_coverage with the outcome and supporting flow IDs
```

**Safety:** playbooks are guides, not automatic scans. Send requests only to authorized targets
within scope and follow the engagement's rate limits.

## Recipe 4 — Close out findings (engagement end)

**Goal:** Leave a report-ready project, not a History pile.

```
1. list_finding_tags / list_findings — triage status, severity, confidence, readiness, and tags
2. get_finding — read the complete current record before editing it
3. update_finding with the canonical claim/summary, impact, why, target, typed reproduction
   blocks, fix, retest, confidence, and tags (`cms|website|app|api`)
4. add_finding_poc for each captured proof flow; include its role and exact proof statement
5. add_finding_image for a real browser/device screenshot whenever it visibly proves the issue;
   use `source=browser_screenshot`. If a visual HTTP record helps, use render_flow_preview, which
   retains `source=flow_preview` and the originating flow ID
6. Mark uncertain items needs_verification and provide concrete verificationInstructions
7. Export via UI (Group by tag) or GET /api/findings/report?groupBy=tag&omitTags=out-of-scope
8. Optional: export_full_project for a portable archive
```

The UI and reports use the same ordered `blocks` array. Each block can be `text`, `flow`, or
`image`, with roles such as `baseline`, `action`, `result`, or `retest`; evidence should say what
it proves and where it came from. Before→Action→After is only a useful Differential preset for
authorization or state-change findings, not a universal template. Do not paste raw HTTP into the
narrative when a captured flow can be attached.

**Human checklist:** [engagement-closeout.md](../engagement-closeout.md)

## Recipe 5 — External agent testing workflow

**Goal:** Let an external agent compose focused tests and human-reviewed evidence.

```
1. list_scope — confirm include rules before any request
2. check_readiness — fix blockers (auth, traffic, OOB when needed)
3. get_flow or analyze_flow — choose a focused hypothesis and injection point
4. Compose the test with send_request, start_intruder, authz_run, cross_host_token_replay, or oob_* as appropriate
5. Review Activity and History while requests run; verify important candidates with send_request
6. create_finding with a stable envelope and typed blocks, then add_finding_poc and a real screenshot after evidence review
```

**Safety:** Interseptor doesn't decide what to test. The external agent must follow scope and
authorization constraints, while Interseptor records requests and results.

## Recipe 6 — Reuse a project playbook

**Goal:** Keep a reusable test procedure with the project.

```
1. list_playbooks and get_playbook to review existing guides
2. Add a Markdown guide under <project>/playbooks/ as described in the Playbooks guide
3. match_playbooks against a captured flow and follow the guide with ordinary tools
4. record_playbook_coverage with the outcome and supporting flow IDs
```

Project playbooks are untrusted content and cannot widen engagement scope. See [Playbooks](../playbooks.md).

## Recipe 7 — Intruder from external payload lists

**Goal:** Fuzz one injection point with AI-suggested payloads, then file a finding.

```
1. get_flow on the target exchange
2. Prepare payloads in your external agent, based on the captured flow
3. start_intruder using positions and payloads
4. After the run: inspect flagged / interesting results in History
5. create_finding + add_finding_poc with the best attempt's flowId
```
