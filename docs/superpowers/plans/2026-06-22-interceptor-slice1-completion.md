# Interceptor — Slice #1 Completion Plan (Plan 2 of slice #1)

> **For agentic workers:** implement task-by-task with TDD (write the failing test, see it
> fail, implement, see it pass, commit). Builds on the finished foundation (Plan 1).

**Goal:** Finish slice #1 — take the working HTTP forward proxy + storage foundation and add
**TLS MITM (HTTPS)**, **request intercept + match-&-replace**, a **control plane** (REST + live
event stream) that **serves a browser-accessible UI**, and a **runnable binary** that runs both the
proxy and control listeners. End state: a user opens the UI in a browser and watches/inspects real
captured HTTP **and** HTTPS traffic, toggles intercept, forwards/drops held requests, edits
match-&-replace rules, and changes the proxy listener — all live.

**Spec:** `docs/superpowers/specs/2026-06-22-interceptor-proxy-core-design.md`.

**Already done (Plan 1):** `internal/store` (SQLite flows + settings + content-addressed bodies),
`internal/capture` (tee bodies to disk), `internal/proxy` (HTTP forward proxy + flow capture),
`cmd/interceptor` (HTTP-only binary). All green.

**Key implementation decisions for this plan:**
- **UI transport:** REST for actions + **SSE** (`GET /api/events`) for live server→client push.
  SSE is the robust, dependency-free equivalent of the spec's "control WebSocket" for one-way
  events (the product's *WebSocket capture* feature stays out of scope for slice #1).
- **UI delivery:** one self-contained `index.html` embedded via `//go:embed` and served by the
  control plane as static files — no Node/Vite build step, ships inside the single static binary.
  (A Vite/React rebuild remains the eventual target per spec.)
- **No new cgo.** Minimal/no new deps; TLS via `crypto/tls` + `crypto/x509`, SSE via plain HTTP.

---

### Task 2: `store` — rules, filtered queries, flags
- `Flow.Flags int64` + constants (`FlagIntercepted`, `FlagEdited`, `FlagDropped`, `FlagCaptureError`,
  `FlagTLSFailed`); persist + scan `flags`.
- `rules` table + `Rule{ID, Ord, Enabled, Type, Match, Replace}`; `ListRules/CreateRule/UpdateRule/DeleteRule`.
- `QueryFlowsFilter(FlowFilter)` — method / host-substr / search-substr(path) / status-class / scheme
  + `BeforeID` cursor pagination, pushed to SQL. Keep `QueryFlows(limit)`.
- **Done:** store tests pass incl. rule CRUD, flag round-trip, filtered query.

### Task 3: `internal/tlsca` — local CA + leaf minting
- `LoadOrCreate(dir)` → ECDSA P-256 CA, self-signed, persisted as `ca.crt`/`ca.key`; reload if present.
- `LeafForHost(host)` → per-host leaf signed by CA (SAN host/IP), cached (mutex map).
- `CertPEM()` (download), `TLSConfig()` (GetCertificate via ClientHello SNI).
- **Done:** leaf verifies against CA pool; second `LoadOrCreate` returns the persisted CA.

### Task 4: `internal/intercept` — hold queue + match&replace
- `Engine`: `Enabled()/SetEnabled(bool)`; `Hold(flow,*http.Request) Decision` **blocks** when enabled
  until `Forward(id, editedRaw []byte)` or `Drop(id)`; `Queue()` snapshot; `SetNotifier(func())`.
- `ApplyRules(*http.Request)` applies enabled request-side regex rules (header/body); `SetRules([]Rule)`.
- `Decision{Action: forward|drop, Request *http.Request}` (edited raw re-parsed when provided).
- **Done:** held request blocks then releases on forward/drop; rules rewrite header+body; notifier fires.

### Task 5: `proxy` — CONNECT/TLS MITM + intercept wiring
- `New(st, cap, ca, eng, events)` (ca/eng/events may be nil → graceful degrade; HTTP test passes nil).
- Refactor a shared `forward(flow, req) (*http.Response, error)` core: clone, strip hop headers,
  intercept `Hold` gate (flags), `ApplyRules`, tee req body, RoundTrip, finalize req body.
- `ServeHTTP`: CONNECT → `handleConnect`; absolute-URI → HTTP forward; `r.TLS!=nil` → MITM origin-form.
- `handleConnect`: hijack, `200 Connection Established`, `tls.Server` with `ca` leaf via SNI, loop
  `http.ReadRequest` → `forward` → tee resp body → `resp.Write(conn)` → capture flow (scheme=https).
  If `ca==nil` → 501.
- Emit `events.FlowCaptured(flow)` after insert; honor `Drop` (record dropped flow, close).
- **Done:** HTTPS request through the proxy is forwarded and captured; existing HTTP test still green.

### Task 6: `internal/control` — REST + SSE + serve UI
- `http.Handler` (mux). Endpoints: `GET /api/flows` (filters→`QueryFlowsFilter`), `GET /api/flows/{id}`,
  `GET /api/flows/{id}/raw?side=req|res`, `GET/POST/PUT/DELETE /api/rules*`,
  `GET /api/intercept` + `POST /api/intercept/toggle|{id}/forward|{id}/drop`,
  `GET/PUT /api/settings`, `GET /api/ca.crt`, `GET /api/events` (SSE), `GET /` + assets (embedded UI).
- Implements `proxy.Events` + intercept notifier; maintains SSE client set; broadcasts
  `flow.new` / `intercept.update` / `settings.update`. `Rebinder` hook for proxy rebind.
- **Done:** endpoints return expected JSON; SSE emits a `flow.new` on capture (httptest).

### Task 7: UI — embedded web app
- `internal/control/ui/index.html` (+ `//go:embed`): dark theme matching the Conduit design tokens
  (JetBrains Mono; `--bg/--accent/--fg/--red/...`). Tabs: **Proxy** (live history table with
  method/status coloring + request/response inspector raw/headers/pretty), **Intercept** (toggle,
  hold queue forward/drop with editable raw, match&replace rules CRUD), **Settings** (proxy
  addr/port rebind, intercept toggle, CA download). Live via `EventSource`.
- **Done:** served at `http://127.0.0.1:9966/`, renders, updates live.

### Task 8: `cmd/interceptor` — wire both listeners
- Open store; `tlsca.LoadOrCreate`; build capture + intercept engine + control hub; `proxy.New(...)`.
- Start proxy listener (addr from `proxy.addr` setting, default `127.0.0.1:8080`) behind a manager
  supporting **runtime rebind** (open new first; on failure keep old, return structured error).
- Start control listener `127.0.0.1:9966` serving UI + API; best-effort open browser; graceful
  shutdown of both; update `CHANGELOG.md`.
- **Done criteria for the plan:** `go test ./...` green; `go vet ./...` clean;
  `CGO_ENABLED=0 go build ./cmd/interceptor` succeeds; running the binary serves the UI at
  `http://127.0.0.1:9966/` and captures proxied **HTTP and HTTPS** traffic visible live in the UI.
