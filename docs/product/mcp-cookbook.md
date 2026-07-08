# MCP Cookbook — three recipes for the AI-assisted pentester

*For Priya + Atlas: copy these prompts into your MCP client after connecting
`interceptor mcp` or `POST http://127.0.0.1:9966/mcp`.*

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

Interceptor has no built-in forced-browser (the old `start_discovery` /
`discovery_state` / `suggest_discovery_paths` tools were removed — see
CHANGELOG). Run a real tool instead, pointed **through** the Interceptor
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
