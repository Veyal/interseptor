# Features

The full rundown. For the short version, see the [README](../README.md#what-it-does).

- **Intercepting proxy** for HTTP **and** HTTPS, with on-the-fly TLS interception via a local CA
  (per-host leaf certs minted on demand).
- **Live history** — every flow captured (metadata in SQLite, bodies content-addressed on disk),
  filterable/searchable, with a raw/pretty request & response inspector and right-click filters.
- **Intercept workflow** — hold / forward (with edits) / drop **requests *and* responses**, plus
  ordered **match-&-replace** rules.
- **Repeater** — multi-tab; re-send any request, edit it freely, inspect the response, per-tab history.
- **Intruder** — Sniper / Pitchfork (one payload list per `§` marker) / **Race** (no-payload concurrent
  resends for race conditions), with thread + delay controls, payload processing (url/base64/…),
  **grep-match/extract**, anomaly flagging, attack tabs and run history.
- **Authorization testing** — replay a request as each saved identity (role) and diff for broken
  access control (IDOR). **OOB interaction catcher** for blind SSRF/XXE/SQLi/RCE (off by default — remote targets cannot reach `localhost`; enable in Settings → Scanner when you have a tunnel or public URL).
- **Autonomous AI pentesting ("Autopilot")** — reads captured history, plans and runs **active**
  testing autonomously via Interseptor's own tools, and files **only** machine-verified findings
  through a 4-gate verifier (differential reproduction → adversarial verifier agent → out-of-band
  proof for blind classes → human confirm for Critical/High). Every step lands in Activity with the
  request visible in History — nothing is a black box.
- **Active scanning** — a deterministic active-scan engine (`active_scan`) fires per-class payloads
  at a request's injection points and confirms hits with detectors, independent of Autopilot; extend
  it with your own **active** Starlark checks the same way as passive ones.
- **Mobile device support** — Android (adb-based CA install + proxy config) and iOS (profile-based,
  including jailbroken-device SSH automation) setup for HTTPS interception on real devices.
- **Collaboration & remote access** — scoped/expiring API keys, a key-authorized remote-access mode,
  browser login, one-click Cloudflare tunnel, and additive project pull/push merge for two operators
  sharing a target.
- **Multi-project launcher** — `interseptor launcher` runs a small dashboard that starts/stops
  multiple project instances from one place.
- **AI assist** — BYO-key LLM explains requests, suggests payloads (with Repeater/Intruder routing),
  and summarizes findings; streamed, rendered as Markdown.
- **Scanner** — 12 passive checks (missing CSP/HSTS/`nosniff`/clickjacking headers, wildcard CORS,
  reflected parameters, secrets in bodies, insecure cookies, Basic-auth & version disclosure, …),
  exportable as a **Markdown findings report**.
- **Custom checks** — extend the scanner with your own passive checks in sandboxed **Starlark**
  (drop a `.star` file in `~/.interseptor/checks/`). See the
  [check-authoring guide](custom-checks.md) and [`examples/checks/`](../examples/checks/).
- **Target scope** — include/exclude rules that focus history, the intercept gate, and the scanner.
- **WebSocket** capture (`ws://`/`wss://` per-frame) **and replay** (a WebSocket Repeater).
- **Session / auth injection** — auto-apply an `Authorization`/`Cookie` to every Repeater & Intruder
  send, plus a **token macro** (CSRF/re-auth: fetch a value from a refresh request, inject per send)
  and a **login macro** (record a login flow, refresh session headers, auto re-auth on 401).
- **Import / export** — HAR in and out, plus portable **project** bundles (flows + rules + scope +
  settings).
- **BYO-key AI assist** — explain a request, suggest payloads, or summarize findings via your own
  **Anthropic** or **OpenRouter** key (off until you set one; the exchange is sent only on request).
- **API & MCP** — a REST control API + SSE event stream and a full **Model Context Protocol** server
  (92 tools, stdio **and** Streamable-HTTP) so an agent or script drives the same core as the UI. See
  [API & MCP](api-and-mcp.md).
