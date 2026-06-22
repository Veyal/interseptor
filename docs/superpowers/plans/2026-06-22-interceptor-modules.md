# Interceptor — Product Modules Plan (Plan 3)

> TDD, module by module. Each module: backend logic (unit-tested) → control API → UI tab → commit.
> Builds on the working slice #1 core (store, capture, tlsca, intercept, proxy, control, UI).

**Goal:** Build the remaining product modules from the Conduit design as *real, working* features on
the Go core (not the design's client-side simulation): **Repeater**, **Intruder**, **Scanner**,
**WebSocket frame capture**, and the **API** module. Each reuses the existing store/capture/raw-render
infrastructure where possible.

**Design source of truth:** `Conduit.dc.html` (module layouts, fields, labels) + `screenshots/`.

**Shared building block — reuse `flows` for sent requests.** Repeater/Intruder send real requests and
store them as `flows` tagged with a flag (`FlagRepeater` / `FlagIntruder`), so the existing
`/api/flows/{id}/raw` inspector and content-addressed body store work unchanged. `QueryFlowsFilter`
gains `RequireFlags` / `ExcludeFlags`; the Proxy history excludes sent requests, each module view
requires its own flag.

---

### Module 1: Repeater
- **Backend** `internal/sender`: `Send(req spec) (*store.Flow, error)` — build an `*http.Request` from
  {method, url, headers, body}, send via a dedicated client (HTTPS, `InsecureSkipVerify` — a pentest
  tool talks to targets with bad certs), tee req/res bodies into the store, persist a `FlagRepeater` flow.
- **Control**: `POST /api/repeater/send` → returns the flow; `GET /api/repeater/history` (RequireFlags=Repeater).
- **UI**: Repeater tab — method + URL + headers + body editors, Send, response (raw/pretty via the
  existing raw endpoint), history list. Right-click history row → **Send to Repeater** (prefill + switch tab).
- **Done:** sending a real request returns a captured flow; history lists repeater sends; proxy history excludes them.

### Module 2: Intruder
- **Backend** `internal/intruder`: parse `§…§` positions in a raw request template; **Sniper** (vary one
  position at a time over a payload list) and **Pitchfork** (walk lists in parallel). Run async, one attack
  at a time; each request sent via `internal/sender`; collect `{payload, status, length, timeMs, flagged}`.
- **Control**: `POST /api/intruder/start` {template, payloads, attackType, target}; `GET /api/intruder/state`
  (running + results); SSE `intruder.update`. Cap total requests (log if capped).
- **UI**: Intruder tab — raw template (with `§` markers), payload list(s), attack-type buttons, Start,
  results table (payload/status/length/time). Right-click history → **Send to Intruder**.
- **Done:** a Sniper attack over N payloads produces N result rows with real statuses/lengths.

### Module 3: Scanner (passive)
- **Backend** `internal/scanner`: `Analyze(*store.Flow, body access) []Issue` — real passive checks:
  cleartext password in request body, session token/JWT in response body, verbose error/trace-id
  disclosure, missing CSP, missing HSTS (on https), wildcard CORS (`Access-Control-Allow-Origin: *`),
  `Set-Cookie` without `Secure`/`HttpOnly`, `Server`/`X-Powered-By` version disclosure. Each Issue:
  `{severity, title, target, detail, evidence, fix, flowID}`. Dedup by (title, target).
- **Control**: `POST /api/scanner/run` (scan all stored flows) / `GET /api/scanner/issues`. Persist issues
  in a `scan_issues` table.
- **UI**: Scanner tab — issues grouped/sorted by severity, detail pane (title/target/detail/evidence/fix).
- **Done:** scanning seeded flows yields the expected issues at the right severities.

### Module 4: WebSocket frame capture
- **Backend**: replace the raw splice in `proxy.tunnelUpgrade` with a frame-aware relay that parses RFC
  6455 frames (handles client masking), records each `{flowID, dir, opcode, len, preview}` to a
  `ws_frames` table, and forwards bytes verbatim. Bounded preview per frame.
- **Control**: `GET /api/flows/{id}/ws` → frames for a websocket flow; SSE `ws.frame`.
- **UI**: Proxy tab gains a **WebSockets** sub-view; selecting a `FlagWebSocket` flow shows its frames.
- **Done:** frames sent over a tunneled ws echo are recorded with direction + payload preview.

### Module 5: API module
- **Backend** `internal/apikeys` (reuse store): generate/list/revoke API keys (random token, hashed at
  rest) in an `api_keys` table.
- **Control**: `GET/POST/DELETE /api/keys`; `GET /api/reference` (machine-readable list of control
  routes); MCP surfaced as a documented endpoint stub (`/api/mcp` returns capability descriptor) — full
  MCP server deferred with a logged note.
- **UI**: API tab — **Keys** (create/revoke), **REST** (route reference), **MCP** (descriptor + note).
- **Done:** create→list→revoke a key; reference lists the real routes.

## Done criteria for the plan
- `go test ./...` green; `go vet ./...` clean; `CGO_ENABLED=0 go build` OK.
- Each module reachable and functional in the UI; Repeater/Intruder send real traffic captured as flows;
  Scanner flags real issues; WebSocket frames captured; API keys manageable.
