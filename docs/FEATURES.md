# Features

User-facing capabilities in the current release. For a tour of the menus, see the [workspace guide](workspace.md).

- **Intercepting proxy** for HTTP **and** HTTPS, with on-the-fly TLS interception via a local CA
  (per-host leaf certs minted on demand).
- **Live history** — every flow captured (metadata in SQLite, bodies content-addressed on disk),
  filterable/searchable, with a raw/pretty request & response inspector and right-click filters.
  Search supports metadata, headers, tags, and bounded body matching, plus deterministic saved
  Starlark predicates for custom Anywhere searches. See [history search](history-search.md).
- **Intercept workflow** — hold / forward (with edits) / drop **requests *and* responses**, plus
  ordered **match-&-replace** rules.
- **Repeater** — sandboxed HTML Render and raw/pretty response views; multi-tab; re-send any request, edit it freely, inspect the response, and keep every send with its owning tab across request edits, tab switches, and reloads until that tab closes (long histories expose older sends in bounded rendering batches).
- **Intruder** — Sniper / Pitchfork (one payload list per `§` marker) / **Race** (no-payload concurrent
  resends for race conditions), with thread + delay controls, payload processing (url/base64/…),
  **grep-match/extract**, anomaly flagging, attack tabs and run history.
- **Authorization testing** — replay a request as each saved identity (role) and diff for broken
  access control (IDOR). **OOB interaction catcher** for blind SSRF/XXE/SQLi/RCE (off by default — remote targets cannot reach `localhost`; enable in Settings → Scanner & OOB when you have a tunnel or public URL).
- **External agent orchestration** — MCP exposes deterministic capture, replay, mutation, scope,
  passive scanning, evidence, and finding tools. External AI or automation owns reasoning and sequencing;
  Interseptor records every request and result in History, Activity, and findings.
- **Mobile device support** — Android (adb-based CA install + proxy config) and iOS (profile-based,
  including jailbroken-device SSH automation) setup for HTTPS interception on real devices.
- **Collaboration & remote access** — scoped/expiring API keys, a key-authorized remote-access mode,
  browser login, one-click Cloudflare tunnel, and additive project pull/push merge for two operators
  sharing a target.
- **Multi-project launcher** — `interseptor launcher` runs a small dashboard that starts/stops
  multiple project instances from one place.
- **External AI integration** — connect an AI assistant through MCP or REST/SSE. Interseptor
  supplies tools and evidence; model calls, reasoning, and sequencing stay outside the binary.
- **Scanner** — built-in passive checks (missing CSP/HSTS/`nosniff`/clickjacking headers, wildcard CORS,
  reflected parameters, secrets in bodies, insecure cookies, Basic-auth & version disclosure, …),
  exportable as a **Markdown findings report**.
- **Custom checks & rule packs** — extend the passive scanner with sandboxed **Starlark** checks
  (drop a `.star` in `~/.interseptor/checks/`), install **official packs** from Scanner → Checks
  (or upload a `.tar.gz`), and share community packs. See
  [custom checks](custom-checks.md), [rule packs](rule-packs.md), and [`examples/checks/`](../examples/checks/).
- **Message codecs** — project-scoped Starlark encrypt/decrypt for app payloads (AES-ECB helpers,
  Decoded view in History/Repeater/Intercept, opt-in re-encode on send). See
  [message codecs](message-codecs.md) and [`examples/codecs/`](../examples/codecs/).
- **Signed rule packs** — ed25519 publisher signatures on pack install (plus manifest sha256). See
  [rule packs](rule-packs.md).
- **Guided content discovery** — ferox/ffuf through the proxy (Map → Discovery); soft-404 clustering
  on Map. See [content discovery](content-discovery.md).
- **HTTP/2 upstream** — prefer h2 to origins; MITM client leg remains HTTP/1.1. See [HTTP/2](http2.md).
- **Target scope** — include/exclude rules that focus history, the intercept gate, and the scanner.
- **WebSocket** capture (`ws://`/`wss://` per-frame) **and replay** (a WebSocket Repeater).
- **Session / auth injection** — auto-apply an `Authorization`/`Cookie` to every Repeater & Intruder
  send, plus a **token macro** (CSRF/re-auth: fetch a value from a refresh request, inject per send)
  and a **login macro** (record a login flow, refresh session headers, auto re-auth on 401).
- **Import / export** — HAR in and out, Burp Suite **Save items** XML migration, Postman Collection
  v2.0/v2.1 JSON import into labeled Repeater tabs (with optional Postman environment resolution),
  plus portable **project** bundles (flows + rules + scope + settings). Imports prepare editable
  requests and warnings; they do not fabricate History evidence. Postman remains useful for
  authoring and running collections; Interseptor is the easier place to inspect live traffic, replay
  with session state, mutate with Intruder, and preserve pentest evidence.
- **Project vault** — always-on archive store (`interseptor vault`) for multi-device backup / import /
  merge (Tailscale Serve). See [vault](vault.md).
- **Model-free core** — no provider keys, built-in chat, or autonomous pentest loop. Use any
  external model or agent that supports MCP, with deterministic Interseptor tools enforcing scope
  and recording evidence.
- **Findings and reporting** — searchable Overview, Evidence, Remediation, and Review sections;
  multiple affected targets; screenshot provenance; CVSS v4 evaluation; explicit readiness checks;
  revision comparison and restore; and Final or Draft report export. Tags support filtering and
  report grouping. See [Findings and reporting](findings-and-reporting.md).
- **API & MCP** — a REST control API + SSE event stream and a full **Model Context Protocol** server
  (stdio **and** Streamable-HTTP) so an agent or script drives the same core as the UI. See
  [API & MCP](api-and-mcp.md).
- **Notes** — project-scoped Markdown with Edit/Preview, image paste, autosave, and save recovery.
  See [Workspace guide](workspace.md#notes).
- **Activity** — inspect recorded agent actions, intent, results, and links to captured flows.
  See [Workspace guide](workspace.md#activity).
- **Decoder** — inspect common text encodings with copyable output, separate from stored Message codecs.
  See [Workspace guide](workspace.md#decoder).
- **Map** — tree and graph views of observed hosts and endpoints, with filters and collapsible groups.
  See [Workspace guide](workspace.md#scanner-and-map).
- **Settings** — searchable Network, Testing, and System sections with save state and recovery.
  See [Settings](settings.md).
- **Session inspector** — a passive timeline and role comparison of selected History captures,
  with redacted observations and explicitly limited conclusions. See [Workspace guide](workspace.md#session-inspector).
