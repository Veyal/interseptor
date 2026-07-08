# PRD-0002 — Active scanning (with and without AI)

**Status:** Shipped (Phase 1, v0.2.0) · **Owner:** veyal · **Depends on:** scope, sender, scanner, checkscript, MCP

## 1. Problem & intent

Interseptor's scanner today is **passive** — it inspects traffic that already happened. The biggest
capability gap vs Burp/ZAP is **active scanning**: deliberately *sending crafted requests* to a target
to confirm vulnerabilities (reflected XSS, SQLi, SSTI, open redirect, …) rather than just guessing
from observed responses.

**Hard requirement (from the product owner): active scanning must work both _without AI_ and _with
AI_.** These are not two engines — they are one engine with two drivers:

- **Without AI (deterministic):** a built-in engine enumerates injection points on an in-scope
  request, fires a fixed payload set per vuln class, and confirms with deterministic detectors. Fully
  reproducible, no key required. This is the default everyone gets.
- **With AI (operated):** the AI drives that *same* engine through MCP — it prioritizes targets,
  crafts context-aware payloads, interprets ambiguous responses, and chains findings — and can fall
  back to bespoke probes via `send_request`. The AI is a smarter operator, **not** a replacement for
  the deterministic core.

> One engine, one API, one findings store. AI sits on top; nothing requires it.

## 2. Non-negotiable: this sends attack traffic

Active scanning is the one feature that **transmits potentially harmful requests to third-party
systems**. Safety is a design constraint, not a feature:

- **Off by default**, never auto-runs. Each run needs an **explicit, per-run consent** ("this sends
  crafted requests — only against systems you're authorized to test").
- **Scope-gated:** only fires at **in-scope** hosts (reuses target scope; out-of-scope is refused).
- **Non-destructive by default:** payloads are detection-oriented (unique markers, time-based,
  error-signature) — never `DROP TABLE`, never state-changing payloads. A clear caveat that *any*
  injection can still have side effects.
- **Bounded:** concurrency cap, per-host rate limit, max requests per run, and a **kill switch**.
- **Auditable:** every probe is recorded as a flow (tagged), and each finding links the exact
  confirming request+response as evidence.
- Honors the README's responsible-use notice.

## 3. Architecture (fits existing seams)

```
flow (in scope) ──▶ injection points ──▶ for each point × check:
                                            mutate request ─▶ sender.Send ─▶ response
                                                                   │
                                                              detector(resp) ─▶ Issue (+ evidence flow)
```

- **`internal/activescan`** (new): the engine.
  - `InjectionPoint`: a place to inject — query param, body param (form/JSON), header, cookie, path
    segment. Extracted from a `store.Flow`.
  - `Check` interface: `Class() string`, `Payloads(point) []Payload`, `Detect(baseline, resp) (*Finding, bool)`.
  - `Engine.Run(req, points, checks, opts)`: mutates, sends via the existing **`sender`** (so probes
    are recorded, session-auth is applied, TLS is lenient), runs detectors, returns findings + the
    confirming flow id for each.
  - A baseline request (unmutated) is sent once for differential checks (length/status/timing deltas).
- **Reuse, don't rebuild:** `sender` (sending + capture), `scope` (the gate), `store` (findings +
  evidence flows), `checkscript` (custom checks, phase 3). The active findings land in the **same
  `store.Issue`** table the passive scanner uses, with a new `FlagActiveScan` on evidence flows.
- **Concurrency:** worker pool bounded by `opts.Concurrency`; per-host token bucket; `context` for the
  kill switch.

## 4. Built-in checks (v1 set — high signal, low noise)

| Class | Payload idea | Detection (deterministic) |
|---|---|---|
| **Reflected XSS** | unique marker `itx<rand>'"<>` | marker reflected **unencoded** in an HTML context |
| **SQL injection (error)** | `'`, `"`, `')` | DB error signatures (MySQL/Postgres/MSSQL/Oracle/SQLite) appear |
| **SQL injection (boolean)** | `' AND 1=1--` vs `' AND 1=2--` | response length/status differs consistently |
| **SSTI** | `{{7*7}}`, `${7*7}`, `<%=7*7%>` | `49` appears where the literal didn't |
| **Open redirect** | `https://canary.invalid` in redirect-y params | `Location`/meta-refresh points off-host to the canary |
| **Path traversal** | `../../../../etc/passwd`, encoded variants | `root:x:0:0:` (or `[fonts]` on win) in the body |
| **OS command injection** | time-based `;sleep 7` / `` `sleep 7` `` | response time jumps ~7s vs baseline (timing) |

Each check ships with a clear title, severity, evidence (the marker + a snippet), and a remediation
line, identical in shape to the passive findings. Start with **reflected XSS + SQLi(error) + open
redirect + SSTI** (the four highest-signal, lowest-false-positive) and grow.

## 5. The two modes, concretely

### 5a. Deterministic (no AI) — the default
UI: Scanner tab → **Active** → pick a target (a selected flow, or "all in-scope") → consent → run.
Or `POST /api/activescan/start`. The engine runs §4 over every injection point and reports confirmed
findings with evidence. Reproducible; no key.

### 5b. AI-operated — same engine, smarter driver
The AI (via MCP, using the **existing** stdio/HTTP transport) does what a human tester does:
1. `list_flows` / `analyze_flow` → pick promising targets and parameters.
2. `active_scan` (new tool) → run the deterministic engine on a flow/point and read confirmed findings.
3. For ambiguous or context-specific cases, craft tailored probes with **`send_request`** and judge
   the response itself (e.g., "this 302's `Location` echoes my param but only the path — try a
   protocol-relative `//canary` payload").
4. Chain + summarize (`scan_report`, the BYO-key assist's "summarize").

Optional assist hooks (BYO-key): "suggest payloads for this parameter" and "triage these active
findings (real vs noise)". **All of this is additive** — pull the AI out and 5a still fully works.

## 6. Custom active checks (phase 3, extends the Starlark standard)

Passive custom checks are sandboxed (no network) — good. For **active** custom checks we keep that
guarantee by splitting responsibility: the check provides payloads and inspects responses; the
**engine** does the sending. A check declares:

```python
CLASS = "ssti"
def payloads():            # the engine sends these into each injection point
    return ["{{7*7}}", "${7*7}"]
def detect(point, sent, response):   # response is read-only, like passive checks
    return finding("high", "SSTI", evidence="49") if "49" in response.res_body else None
```

The check never opens a socket; the sandboxed-and-shareable property is preserved, and the community
(and the AI via `save_check`) can author active checks too.

## 7. Surfaces

- **REST/SSE:** `POST /api/activescan/start` `{flowId? , inScope? , checks?[] , maxRequests , concurrency , consent:true}`; `GET /api/activescan/state` (progress + findings); `POST /api/activescan/stop` (kill switch). Progress streams over the existing SSE as `activescan.update`.
- **MCP:** `active_scan` (run against a flow/scope, returns confirmed findings), `active_scan_state`, `active_scan_stop`. Plus the AI keeps `send_request` for bespoke probes. (≈ +3 tools.)
- **UI:** an **Active** sub-mode in the Scanner tab: target picker, an arming **consent banner** (red), live progress + a stop button, and findings that open the confirming request/response. Off until consented.

## 8. Phased delivery

1. **Phase 1 — deterministic core (TDD):** `internal/activescan` (injection points + engine + the 4
   starter checks + detectors), scope/consent gate, `POST /api/activescan/*` + SSE, the `active_scan`
   MCP tool, and the Scanner **Active** UI with consent. Probes recorded as evidence flows.
2. **Phase 2 — AI operation:** MCP ergonomics so the AI drives well, the two assist hooks, and a
   `docs/active-scanning.md` "drive it with AI" guide.
3. **Phase 3 — custom active checks:** the `CLASS`/`payloads()`/`detect()` Starlark contract above,
   wired through the existing checks editor + `save_check`.
4. **Later:** more classes (CRLF, SSRF w/ collaborator, XXE, header injection), light crawling to
   discover endpoints, auth-aware scanning, and a tuneable aggressiveness profile.

## 9. Decisions (resolved)

1. **Aggressiveness:** ship **all 7 classes** in v1 (XSS, error-SQLi, boolean-SQLi, SSTI, open
   redirect, path traversal, timing OS-command-injection). Timing checks are flagged lower-confidence.
2. **Consent:** **session-level arm** — one "I'm authorized to test in-scope targets" toggle that
   persists until turned off (and resets on restart); active scans refuse to run while disarmed.
3. **Targeting:** support **both** a single selected flow *and* "scan all in-scope" (bulk), bounded by
   the request budget + per-host rate limit + kill switch.

## 10. Out of scope (for now)
Full active crawl/spider, authenticated multi-step sequences (depends on the session/login-macro
roadmap item), DoS-style fuzzing, and any destructive payloads.
