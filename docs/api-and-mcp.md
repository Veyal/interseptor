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

The MCP `initialize` result includes the running `apiVersion`, a stable `schemaVersion`, and a
`schemaHash` computed from the live tool registry. Clients that explicitly send `schemaVersion` or
`schemaHash` during initialization receive a reconnect diagnostic when their contract is stale;
clients that omit these optional fields remain compatible. `GET /api/mcp/capabilities` exposes the
same metadata plus the currently supported create and update finding fields. This endpoint is a
diagnostic capability surface, not a promise that a client-side schema cache can be invalidated
without reconnecting.

For a task-oriented walkthrough (recon → auth → scan → record findings), see
[docs/product/mcp-cookbook.md](product/mcp-cookbook.md).

## Control API

The full REST surface is documented at runtime: `GET /api/reference` (or the **Settings → API & MCP**
section) — including the request/response body shape for every mutating route, not just its method
and path. Live updates stream over Server-Sent Events at `GET /api/events`. Highlights:
`/api/flows`, `/api/repeater/send`, `/api/intruder/start`, `/api/scanner/run`, `/api/scope`,
`/api/session`, `/api/ws/send`, `/api/export/{har,project}`, `/api/settings`.

Burp Suite migration uses `POST /api/import/burp` with a raw **Save items** XML export. The endpoint
streams entries into History and returns `{imported, skipped}`. Native `.burp` project files are not
an accepted interchange format.

Postman Collection v2.0/v2.1 JSON uses `POST /api/import/postman`. Send the raw collection, or
`{collection, environment}` when an exported Postman environment should resolve `{{variables}}`.
The response is `{name, requests, unresolved, warnings, skipped}`; each request includes its folder
label, method, URL, headers, body, and any request-specific warnings. Folders are flattened into
editable Repeater tabs. Common bearer, basic, API-key, and OAuth 2 auth plus raw, URL-encoded,
multipart, and GraphQL bodies are prepared where possible; unresolved variables remain visible and
unsupported features (such as local file bodies) are reported for review. Import intentionally
creates no History flows because a collection is a plan, not captured evidence; sending a tab is
what creates live replay evidence.

### Findings: one canonical evidence format

Findings are persistent, curated records shared by the UI, REST API, MCP tools, and report
export. The stable envelope is:

| Field | Use |
| --- | --- |
| `title`, `summary` | Short, report-ready claim and one-sentence statement of the vulnerable behavior |
| `severity`, `confidence`, `cwe`, `cvss`, `environment` | Review and prioritization metadata |
| `target`, `targets` | Primary summary and ordered endpoints: URL, methods, role, variant, relation, flow_ids, and documented setup/chain evidence exceptions |
| `proofReview` | execution (`demonstrated`, `prerequisite_only`, `not_executed`), reason, and visual-proof requirement |
| `cvssScore`, `cvssRating`, `cvssNomenclature` | Calculated, read-only CVSS v4 results; severity mismatch remains a readiness gap |
| `impact` | What an attacker gains or the business/security consequence |
| `why` | The broken security property or trust boundary |
| `blocks` | Ordered typed reproduction and evidence records |
| `fix`, `retest` | Remediation at the failed boundary and the expected secure negative case |
| `status`, `verificationInstructions`, `tags` | Review workflow and report scoping |

Use `blocks` as the source of truth for reproduction. A block is `text`, `flow`, or `image` and
may carry `role` (`context`, `setup`, `baseline`, `action`, `result`, `control`, `retest`, or
`observation`), `proof` (the exact claim established), and `source`. Flow evidence uses
`source=captured_flow` and `sourceFlowId`; generated HTTP previews use `source=flow_preview`.
See [image source classifications](findings-and-reporting.md#evidence-rules) for image evidence values.
Prefer a real browser/device screenshot when it visibly proves the issue, and attach the captured
flow as the inspectable request/response record. The API validates and stores images by content
hash (maximum 5 MiB); it does not store image data inside the finding body.
The complete canonical `blocks` body is limited to 1 MiB after every create, partial update, flow
attachment, or image attachment. Over-limit mutations fail atomically. A preserved `missing` flow
block remains visible but cannot resolve to an unrelated local flow during collaboration merge.
Collaboration imports preflight canonical and table-only evidence before publishing local rows.
The scalar report envelope (title, summary, target, impact, cause, remediation, retest, review
instructions, and legacy `detail`/`evidence` compatibility copies) has a separate aggregate 1 MiB
limit. The store evaluates retained plus changed fields, so several individually small AI/API updates
or a canonical body paired with distinct legacy text cannot bypass the limit.

The UI file picker, paste, and drop paths record `source=operator_upload` because Interseptor cannot
infer how an external file was captured. MCP image uploads use the same conservative default; set
`source=browser_screenshot` only when the caller knows the bytes came from a real browser/device
capture. This preserves provenance without making screenshots harder to attach.

`Before → Action → After` is not a required report shape. It is an optional Differential preset
for authorization or state-change comparisons. For other findings, choose the shortest roles that
make the claim reproducible (for example `observation → result`, or `setup → action → result`).

REST endpoints:

```text
POST   /api/findings                         create (title required; blocks or legacy body)
GET    /api/findings/{id}                    read canonical finding and readiness
PATCH  /api/findings/{id}                    partial update (blocks or legacy body)
POST   /api/findings/{id}/flows              attach {flowId, role?, note?, proof?, position?}
DELETE /api/findings/{id}/flows/{flowId}     detach a flow
POST   /api/findings/{id}/images             attach {data, mime?, caption?, role?, proof?, source?, sourceFlowId?, position?}
POST   /api/findings/{id}/flow-preview       render and attach a labeled HTTP PNG
GET    /api/findings/report                  md (default), html, or json export
```

For example, an AI can create the narrative without embedding raw HTTP, then attach proof:

```json
{
  "title": "Cross-account record access",
  "summary": "A user can read another account's record by changing the object identifier.",
  "severity": "High",
  "confidence": "firm",
  "target": "api.example.com/v1/accounts/{id}",
  "impact": "An authenticated attacker can read another customer's record.",
  "why": "The object-level authorization check does not bind the record to the session.",
  "blocks": [
    {"type":"text", "role":"baseline", "md":"Open a record owned by the current account."},
    {"type":"text", "role":"action", "md":"Change `{id}` to another account's identifier."},
    {"type":"text", "role":"result", "md":"The response contains the other account's record."}
  ],
  "fix": "Enforce object ownership authorization before returning the record.",
  "retest": "Repeat with a different account identifier; expect 403 or an equivalent non-disclosing response."
}
```

Use `get_finding` before an AI update so it does not replace a concurrent human edit. MCP maps
`create_finding`, `get_finding`, `update_finding`, `add_finding_poc`, `add_finding_image`,
`render_flow_preview`, and `export_report` to these same contracts. `body` remains accepted for
legacy clients, but must not be sent together with `blocks`; old `ready`/`missing` fields remain
alongside structured `readiness` for compatibility. Report export supports Markdown, self-contained
HTML, and JSON (`format=md|html|json`); use self-contained HTML when screenshot pixels must travel
with the report. PDF is not an API format.
Self-contained HTML embeds at most 5 MiB per image and 8 MiB across the report. An image that exceeds
either bound remains explicitly marked unavailable in the export instead of creating an unbounded
document or silently depending on the live control API.
Raw request/response evidence is limited to 64 KiB per side after decoding; compressed streams also
enforce bounded decoder windows and memory before output is read.

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

Finding environments accept `production`, `staging`, `development`, `testing`, `local`, and legacy
`prod`. Unsupported values fail validation instead of being silently stored as local. See
[capability readiness and affected targets](findings-and-reporting.md#capability-based-report-readiness)
for the complete report checklist, metadata, and backward compatibility behavior.


### Finding review tools

The UI and MCP share the report-quality and evidence contracts:

- `finding_readiness` returns actionable field/capability checks; `export_report` accepts `mode=final`
  to enforce them or `mode=draft` to retain incomplete work.
- `preview_finding_targets` previews deduplication and optional path templates without saving.
- `normalize_finding_targets` applies reviewer-approved suggestion indexes from that preview to a saved finding
  (`dryRun` defaults to true; `POST /api/findings/{id}/normalize-targets`).
- `evaluate_finding_cvss` previews a CVSS v4 vector without updating a finding.
- `list_finding_revisions`, `get_finding_revision`, and `restore_finding_revision` expose immutable
  finding history and recovery. Restore appends a revision and cannot reconstruct separately purged
  traffic. Historical snapshots require the same care as current evidence.

Capability declarations under `proofReview.claims` are reviewer observations linked to retained
artifacts, never automatic confirmation of exploitation. Image provenance is server-stamped and is
separate from a reviewer's browser/device classification. See [Findings and reporting](findings-and-reporting.md).
