# API & MCP

Interseptor exposes deterministic security operations over two machine-facing surfaces, so a human
and an external agent can drive the *same* engine at the same time. Interseptor doesn't provide a
model, provider integration, or autonomous decision loop. The external agent owns reasoning and
sequencing.

## Connect an external agent with MCP

Interseptor ships a **Model Context Protocol** server so an AI assistant can operate the proxy with
the same capabilities as the UI. Run the app, then connect your MCP client one of two ways:

**stdio** (Claude Desktop / Claude Code) — point your client at the `mcp` subcommand:

```jsonc
{
  "mcpServers": {
    "interseptor": { "command": "interseptor", "args": ["mcp"] }
  }
}
```

**Streamable-HTTP** (hosted/remote agents) — `POST` JSON-RPC to `http://127.0.0.1:9966/mcp`
(stateless; no subprocess needed).

Both expose the same tool registry as the control plane — reading flows (`list_flows`, `get_flow`, `analyze_flow`,
`flow_as_curl`), replaying/fuzzing (`send_request`, `start_intruder`, `ws_send`), scanning
(`run_scanner`, `scan_report`), intercept/rules/scope control, and `set_session` — with bounded
results so large bodies don't blow the agent's context. Each tool's JSON Schema documents its
arguments (types, required fields, accepted variants) inline, so an agent can read a tool's
definition instead of guessing. The **Settings → API & MCP** section shows a copy-paste config
and the live tool list.

For a task-oriented walkthrough (recon → auth → scan → record findings), see
[docs/product/mcp-cookbook.md](product/mcp-cookbook.md).

## Control API

The full REST surface is documented at runtime: `GET /api/reference` (or the **Settings → API & MCP**
section) — including the request/response body shape for every mutating route, not just its method
and path. Live updates stream over Server-Sent Events at `GET /api/events`. Highlights:
`/api/flows`, `/api/repeater/send`, `/api/intruder/start`, `/api/scanner/run`, `/api/scope`,
`/api/session`, `/api/ws/send`, `/api/export/{har,project}`, `/api/settings`.

### History search API

`GET /api/flows` supports `searchScope=anywhere|body|id` and `savedSearch=<name>`. Anywhere search checks flow metadata, headers, tags, and bodies, with an 8,000-candidate cap and 256 KiB per-body read cap. Responses expose `searchNote` when a search reaches its scan limit and `truncated` when the result page exceeds `limit`.

Saved deterministic Starlark searches use these routes. They inspect at most 64 filtered flows, expose at most 64 KiB per body and 8 MiB of body data per request; this remains separate from Anywhere search's 8,000-candidate cap:

```text
POST   /api/flow-searches/test
GET    /api/flow-searches
POST   /api/flow-searches
GET    /api/flow-searches/{name}/source
PUT    /api/flow-searches/{name}
DELETE /api/flow-searches/{name}
```

POST and PUT accept JSON `{name, scope, script}`. `scope` normalizes to `anywhere`, `body`, or `id`. The script must define `match(flow)` and return bool. Saved searches are project-scoped and persist in project settings. Full fields, helpers, limits, and examples live in [History search](history-search.md).

Auth and trust rules for both surfaces (loopback vs. key-authorized remote access, scoped keys,
CSRF handling) are covered in [Security model](architecture.md#security-model).
