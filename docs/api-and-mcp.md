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
`source=captured_flow` and `sourceFlowId`; generated HTTP previews use `source=flow_preview`; generated evidence renders use `source=evidence_render` with a server-stamped `sourceRef` such as `intruder:<runId>`.
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
GET    /api/intruder/attacks                 recent recorded Intruder runs (last 20 kept)
GET    /api/intruder/attacks/{id}            one recorded run by runId; survives the next start and restarts
GET    /api/intruder/attacks/{id}/render.png ?kind=timeline|distribution|race|strip&width=&unmask=1&expected=N&format=json&png=1 evidence render PNG (unattached; X-Render-Alt/X-Render-Summary headers)
GET    /api/intruder/attacks/{id}/render     alias of render.png (MCP render_intruder_preview)
GET    /api/evidence-render                  ?kind=...&runId|flowIdA,flowIdB|flowIds|findingIds&includeBody=1 single-endpoint render PNG (MCP render_evidence)
POST   /api/findings/{id}/evidence-render    render and attach {kind, runId?|flowIds?|..., caption?, role?, proof?, position?}
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
`render_flow_preview`, `render_evidence`, and `export_report` to these same contracts. `body` remains accepted for
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
- `redact_value` (and `POST /api/redact`) returns `{len, sha256_prefix, kind}` for a secret so a finding can show
  length and equality without the value. The value is hashed in memory and never stored. Finding writes
  warn when text looks like a JWT, `AIza` key, `$2b$` hash or `Bearer` token.
- `list_finding_revisions`, `get_finding_revision`, and `restore_finding_revision` expose immutable
  finding history and recovery. Restore appends a revision and cannot reconstruct separately purged
  traffic. Historical snapshots require the same care as current evidence.

Capability declarations under `proofReview.claims` are reviewer observations linked to retained
artifacts, never automatic confirmation of exploitation. Image provenance is server-stamped and is
separate from a reviewer's browser/device classification. See [Findings and reporting](findings-and-reporting.md).

### Evidence renders

Evidence renders are deterministic, report-width PNGs drawn in pure Go from recorded data only. Each
carries alt text, a summary line and the footer "Generated by Interseptor from recorded data - not a
browser screenshot". There are eight kinds: `intruder-timeline`, `intruder-distribution`,
`intruder-race`, `intruder-strip`, `authz-matrix`, `flow-diff`, `flow-waterfall` and `finding-chain`.
Without a `findingId`, the MCP tool `render_evidence` returns the PNG as a data URI; with one it attaches
the image atomically with `source=evidence_render` and `sourceRef` (`intruder:<runId>`, `authz:<runId>`).

What is and is not recorded:

- Each Intruder result records `seq`, `worker`, `startUs`/`endUs` (monotonic microsecond offsets from run
  start), `bodyHash` and whitelisted rate-limit headers (`Retry-After`, `X-RateLimit-*`, `RateLimit-*`).
  Runs recorded before this existed render as a ranked-by-completion strip with a "timing not recorded" note.
- `barrier=true` on a repeat run makes workers launch together after all are parked. They still use
  separate connections; there is no single-packet or last-byte synchronisation, and renders say so.
- Flow-based renders (`flow-waterfall`, `flow-diff`) have millisecond precision and only total duration;
  DNS, connect and TTFB are not recorded and are never drawn.
- Claims such as "first 429 at request #N" are computed from recorded status and offsets only. A render
  never says a race or rate-limit bypass is confirmed; it reports counts.
- Secrets are redacted by the adapters (`redact.Text`: header lines, Bearer/Basic schemes, JWTs, long
  opaque tokens and key=value / JSON secrets under snake_case, kebab-case and camelCase names such as
  `accessToken`, `csrf_token`, `otp`, `reset_code`, `?code=482913`). Short standalone values with no
  recognisable key cannot be detected, so values that are often credentials are masked by default instead:
  Intruder payloads (strip) and extracted values (race) are drawn as `[len N #digest]` (equal values stay
  comparable) unless the request passes `unmask=1` (`mask=0`; MCP `unmask`; attach body `unmask`). Flow-diff
  response-body lines are omitted by default (only the changed-line count is drawn); `includeBody=1` draws
  redacted lines and the footer says body excerpts can still show personal data. `expected=N` (race) adds
  the baseline "expected at most N" to the headline.
- Anything target-controlled (header values, URLs, titles, payloads, targets, run ids) is clipped before
  drawing. Characters the embedded fonts cannot draw (CJK and other non-Latin scripts) are drawn as
  `U+XXXX` and the footer says so. `width` is `0` (default 1100) or 640-1600; non-numeric or negative ids
  are a 400. At most four renders run at once (503 when busy) with a 10 s deadline. The JSON form
  (`format=json`) adds the base64 PNG with `png=1` for PNGs up to 1 MiB, otherwise `pngOmitted` and `bytes`.
- Rate-limit wording is descriptive: tiles say `2xx responses`, `throttle statuses (429/403/423)` and
  `5xx/other`; a missing throttle status is reported as "no throttling status observed; body-based
  lockouts are not detected unless a grep pattern was set". Grep matches are counted separately.
- The persisted run record (`GET /api/intruder/attacks/{id}`, table `intruder_runs`, last 20 runs) is raw
  like the flow store: payloads, extracted values and rate-limit headers are not redacted, and it travels in
  full-project archives (it is not part of portable JSON export or peer merge). Only the renders redact.
  Runs over the 4 MiB record cap drop payloads, then rate-limit headers and error text, then keep a head
  and tail with a `truncated` marker in the stored run state.

Provenance: `evidence_render` is generated. It never satisfies the real-visual-proof check, and reports
label it "generated evidence render from recorded data; not browser proof". `sourceRef` is stamped
server-side and immutable, like `sourceFlowId` (the first recorded flow of an Intruder run, the baseline
flow of an authz run, flow A of a diff). The source is deliberately `evidence_render`, a separate value
from `generated_image`, and the run id lives in `sourceRef` (`intruder:<runId>`) rather than in a separate
`attackId` field. Every render also shows its ref in the footer and carries an `Interseptor-Render` PNG
tEXt marker; the store refuses to attach a marked PNG as `browser_screenshot`, `device_screenshot` or
`operator_upload`. Attach a real screenshot with `add_finding_image` when a
finding makes a visual claim.
