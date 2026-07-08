# Interseptor — Improvement Analysis (UX/ease + API/MCP)

*Owner: Product · Last updated: 2026-06-22*

Gap analysis driving the [roadmap](roadmap.md), framed by our intent: a proxy operated by **a
pentester and their AI together**. Two lenses: **the human's ease-of-use**, and **the AI's ability
to operate the tool**. Items marked **▶ now** are being executed this slice.

## A. The AI as an operator (MCP + API)

| # | Gap today | Impact | Recommendation | Pri |
|---|---|---|---|---|
| A1 | **No real MCP server** — `/api/mcp` is only a *descriptor*; an agent can't actually call tools | Blocks the entire intent | **Ship `interseptor mcp`** — a stdio JSON-RPC MCP server exposing the control API as tools (list/get/search flows, send request, intruder, scanner, intercept, rules, ws frames, settings, CA). **▶ now** | P0 |
| A2 | **Hard to connect** — even with a server, users won't know the config | High | UI **MCP tab shows a copy-paste client config** + live connection/health. **▶ now** | P0 |
| A3 | **Large results blow the agent's context** — raw bodies can be tens of KB | High | Tools return **bounded previews by default** with an explicit "full" fetch; `list_flows` returns compact summaries. **▶ now** (designed into the tools) | P0 |
| A4 | **Result predictability** — a few endpoints return bare arrays/strings | Med | Keep every API/tool result a small, documented JSON shape; `GET /api/reference` already enumerates routes | P1 |
| A5 | **No auth on remote use** — API keys exist but aren't enforced; fine on localhost, unsafe if exposed | Med | Enforce API key when bound non-locally / for the future streamable-HTTP MCP transport | P1 |
| A6 | **No "analyze/summarize" helper** — the agent reconstructs context each call | Med | Later: an `analyze_flow` tool returning a compact, decision-ready summary (method/url/status, notable headers, detected params, scanner hits) | P2 |

## B. The human's ease-of-use (UI/UX)

| # | Friction today | Impact | Recommendation | Pri |
|---|---|---|---|---|
| B1 | **Onboarding cliff** — trusting the CA + pointing the client at the proxy is the hardest first step, and there's no in-app guidance | Highest UX lever | A first-run **"Get started" panel**: download-CA button, OS-specific trust hint, proxy address to copy, and a **system-proxy toggle**; dismiss once a flow is captured | P0 |
| B2 | **No global "am I capturing / intercepting?" cue beyond the header dot** | Med | Make proxy/intercept state unmistakable; surface a captured-count and intercept badge prominently | P1 |
| B3 | **History search is path-substring only** | Med | Add full-text search across method/host/path (+ later headers/body) and saved filters; pairs with Target Scope | P1 |
| B4 | **Inspector lacks copy + in-pane search + highlighting** | Med | Add copy-buttons (already have Copy URL/cURL in the row menu), find-in-response, light syntax highlight for JSON | P1 |
| B5 | **No keyboard-driven workflow** | Low-Med | Shortcuts: `/` focus filter, `j/k` move rows, `r` send-to-Repeater, `i` toggle intercept | P2 |
| B6 | **Noise** — third-party hosts flood the history | High | **Target Scope** (PRD-0001) — focuses history/intercept/scanner; the single biggest noise reducer | P0 (roadmap Now) |
| B7 | **Empty states could teach the next action** | Low | Each tab's empty state suggests the next step (capture a flow, add a rule, run a scan) | P2 |

## C. Trust & correctness (table stakes that protect everything)

- Capture must never break forwarding (already designed; keep guardrail tests).
- TLS/secret correctness; idle-RSS regression guard (publish benchmarks — roadmap Now).

## This slice (executing now)

1. **A1 — real MCP server** (`internal/mcp`, `interseptor mcp` subcommand) with bounded-preview tools.
2. **A2 — MCP setup in the UI** (copy-paste config + how-to) and an updated `/api/mcp` descriptor.
3. **A3 — bounded results** baked into the MCP tools.
4. **B1 (partial) — onboarding/MCP-setup ease** surfaced where the user already is.

Target Scope (B6/roadmap), benchmarks, HAR export, and the deeper UX items (B2–B5) follow per the
[roadmap](roadmap.md).
