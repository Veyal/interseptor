# API & MCP

Interseptor exposes everything the UI can do over two machine-facing surfaces, so a human and an AI
agent can drive the *same* engine at the same time.

## Drive it with AI (MCP)

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

Both expose the same **92 tools** — reading flows (`list_flows`, `get_flow`, `analyze_flow`,
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

Auth and trust rules for both surfaces (loopback vs. key-authorized remote access, scoped keys,
CSRF handling) are covered in [Security model](architecture.md#security-model).
