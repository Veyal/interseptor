package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/version"
)

// ---- API keys ----

func (h *metaAPI) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.st.ListAPIKeys()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []store.APIKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *metaAPI) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label     string `json:"label"`
		Scope     string `json:"scope"`     // "full" (default) | "read"
		ExpiresIn int64  `json:"expiresIn"` // seconds from now; 0 = never
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Label == "" {
		in.Label = "key"
	}
	var expires int64
	if in.ExpiresIn > 0 {
		expires = time.Now().UnixMilli() + in.ExpiresIn*1000
	}
	token, key, err := h.st.CreateAPIKey(in.Label, store.NormalizeScope(in.Scope), expires)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "key": key})
}

func (h *metaAPI) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteAPIKey(id); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- REST reference ----

type apiRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Desc   string `json:"desc"`
}

var apiRoutes = []apiRoute{
	{"GET", "/api/flows", "List captured proxy flows (filters: method, host, search, searchScope=body, hasNote=1, scheme, status, before, limit, includeTools=1). By default excludes Repeater/Intruder/ActiveScan (History-shaped); includeTools=1 returns all sources"},
	{"GET", "/api/flows/{id}", "Flow detail (headers, body hashes, flags)"},
	{"GET", "/api/flows/{id}/raw", "Reconstructed raw request/response (?side=req|res)"},
	{"GET", "/api/flows/{id}/body", "Body bytes only (?side=req|res) — for download with MIME extension"},
	{"GET", "/api/flows/{id}/ws", "Captured WebSocket frames for a flow"},
	{"GET", "/api/flows/inscope", "Whether any in-scope traffic exists (paginated; for readiness checks)"},
	{"GET", "/api/params", "Aggregate query/form/JSON parameter names from captured traffic (?host=, ?inScope=1)"},
	{"POST", "/api/ws/send", "WebSocket Repeater: open a socket, send a message, return reply frames. Body: {url, message, binary?, headers?} — headers is \"Key: Value\" lines. Response: wsrepeater result (frames received)"},
	{"POST", "/api/decode", "Decode/encode a string (base64, url, hex, html, jwt, smart). Body: {op, input} — op one of base64encode/base64decode/urlencode/urldecode/hexencode/hexdecode/htmlencode/htmldecode/jwtdecode/smart. Response: {output} or {error} (200 either way)"},
	{"GET", "/api/rules", "List match-&-replace rules"},
	{"POST", "/api/rules", "Create a rule. Body: {ord, enabled, type, match, replace} — type one of req-header/req-body/res-header/res-body; match is a regex"},
	{"PUT", "/api/rules/{id}", "Update a rule. Body: same shape as POST /api/rules"},
	{"DELETE", "/api/rules/{id}", "Delete a rule"},
	{"GET", "/api/intercept", "Intercept state + hold queue"},
	{"GET", "/api/intercept/held/{id}/raw", "Raw bytes of a held intercepted request/response (?side=resp for the response side, else request)"},
	{"POST", "/api/intercept/toggle", "Enable/disable intercept. Body: {enabled}"},
	{"POST", "/api/intercept/filter", "Configure the conditional-intercept regex filter ({enabled,target,pattern})"},
	{"POST", "/api/intercept/{id}/forward", "Forward a held request (optionally edited). Body: {raw?} — full raw HTTP request to substitute; omit to forward unmodified"},
	{"POST", "/api/intercept/{id}/drop", "Drop a held request"},
	{"POST", "/api/intercept/response/toggle", "Enable/disable response interception ({enabled})"},
	{"POST", "/api/intercept/response/{id}/forward", "Forward a held intercepted response (optionally edited)"},
	{"POST", "/api/intercept/response/{id}/drop", "Drop a held intercepted response"},
	{"POST", "/api/repeater/send", "Send a request from Repeater. Body: {method, url, headers?, body?} — headers is \"Key: Value\" lines or a {\"Key\":\"Value\"} object; refuses targets that resolve to Interseptor's own listeners. Response: flow detail (id, status, headers, body hashes)"},
	{"GET", "/api/repeater/history", "Repeater send history"},
	{"POST", "/api/flows/{id}/replay", "Re-send a captured flow's request as a new Repeater flow. Body: {session} — \"current\" applies the active session headers, \"flow\" (default) replays exactly as captured. A GET /replay/{id}?session=... confirm page (copied via the History right-click menu) posts here."},
	{"POST", "/api/intruder/start", "Start a Sniper/Battering/Pitchfork/Cluster attack. Body: {target, template, attackType, payloads, repeat?, threads?, delayMs?, grepMatch?, grepExtract?, processRules?} — template marks fuzz points with §…§, payloads is a list of payload lists. Response: attack state"},
	{"GET", "/api/intruder/state", "Current attack progress + results"},
	{"POST", "/api/scanner/run", "Run passive checks over captured flows (no body)"},
	{"GET", "/api/scanner/issues", "List scanner findings"},
	{"GET", "/api/scanner/report", "Download scanner findings as a Markdown report"},
	{"GET", "/api/activescan", "Active-scan state (armed/running/findings/probe log)"},
	{"GET", "/api/activescan/history", "Active-scan probe history (all FlagActiveScan flows)"},
	{"POST", "/api/activescan/arm", "Arm/disarm active scanning (consent gate). Body: {armed}"},
	{"POST", "/api/activescan/start", "Start an active scan (sends attack payloads). Body: {flowId?, inScope?, arm?, maxRequests?, csrfAware?} — one of flowId/inScope required; inScope:true needs a scope include rule first"},
	{"POST", "/api/activescan/stop", "Stop the running active scan"},
	{"GET", "/api/checks", "List custom Starlark scanner checks (id, source, compile error)"},
	{"GET", "/api/checks/reference", "Custom-check authoring reference (Starlark API, markdown)"},
	{"POST", "/api/checks/test", "Compile + run a check without saving. Body: {source, flowId?} — flowId omitted uses the most recent captured flow. Response: {findings} or {error}"},
	{"POST", "/api/ai/checks/generate", "BYO-key AI: plain-text description → Starlark check source. Body: {description, source?, flowId?} — source is an existing draft to refine. Response: {source, suggestedId} or {error, source, suggestedId}"},
	{"GET", "/api/checks/{id}", "Read a custom check's source"},
	{"PUT", "/api/checks/{id}", "Create/update a custom check (rejected if it doesn't compile). Body: {source}"},
	{"DELETE", "/api/checks/{id}", "Delete a custom check"},
	{"GET", "/api/active-checks", "List custom active (Starlark) checks, their source dir, and disabled builtin ids"},
	{"GET", "/api/active-checks/{id}", "Read one active check's source (builtin template or user override)"},
	{"PUT", "/api/active-checks/{id}", "Save/upsert an active check's Starlark source"},
	{"DELETE", "/api/active-checks/{id}", "Delete a saved active check"},
	{"POST", "/api/active-checks/test", "Compile + dry-run an active check against a flow's injection points (sends real, bounded probes). Response: {finding} or {note}"},
	{"GET", "/api/findings", "List curated findings (optional ?severity=&status=)"},
	{"GET", "/api/findings/report", "Curated findings as a Markdown/HTML report (?format=html; ?issues=1; ?includeBodies=0 to omit full PoC req/res)"},
	{"POST", "/api/findings", "Create a curated finding. Body: {title, severity?, status?, target?, detail?, evidence?, impact?, cvss?, verificationInstructions?, body?, flowIds?} — title required; status may be open|needs_verification|verified|false_positive|wont_fix|fixed; verificationInstructions is what the human should check when status is needs_verification; body is a JSON-blocks array string [{type:'text',md}|{type:'flow',flowId,note}|{type:'image',hash,mime,caption}] for the interleaved narrative; flowIds optionally attaches PoC flows on create. Image bytes must be uploaded via POST /api/findings/{id}/images (not embedded as data/path in body)"},
	{"GET", "/api/findings/{id}", "Get one finding"},
	{"PATCH", "/api/findings/{id}", "Update a finding (only fields sent are changed). Body: any subset of {severity, status, title, target, detail, evidence, impact, cvss, verificationInstructions, body}"},
	{"DELETE", "/api/findings/{id}", "Permanently delete a finding"},
	{"POST", "/api/findings/{id}/flows", "Attach a flow as PoC evidence to a finding. Body: {flowId, note?, position?} — position is a 0-based block index to insert at; omit/-1 appends. Returns 404 if flowId does not exist"},
	{"DELETE", "/api/findings/{id}/flows/{flowId}", "Detach a PoC flow from a finding"},
	{"POST", "/api/findings/{id}/images", "Upload and attach a screenshot/image as finding evidence. Body: {data, mime?, caption?, position?} — data is raw base64 or a data: URL (max 5 MiB); position is a 0-based block index; omit/-1 appends. Response: full finding with enriched image block (url, hash)"},
	{"GET", "/api/findings/images/{hash}", "Serve a content-addressed finding image by sha256 hash"},
	{"GET", "/api/views", "List saved history views"},
	{"POST", "/api/views", "Save the current filters as a named view. Body: {name, data} — data is an arbitrary JSON filter-state blob"},
	{"DELETE", "/api/views/{id}", "Delete a saved view"},
	{"GET", "/api/scope", "List target-scope rules"},
	{"POST", "/api/scope", "Add a scope rule. Body: {action, host?, path?, scheme?, port?} — action is include|exclude; needs at least one of host/path/scheme/port"},
	{"PUT", "/api/scope/{id}", "Update a scope rule. Body: same shape as POST /api/scope"},
	{"DELETE", "/api/scope/{id}", "Delete a scope rule"},
	{"GET", "/api/settings", "Get proxy/intercept settings"},
	{"PUT", "/api/settings", "Update settings (rebinds proxy/control listeners). Body: any subset of {proxyAddr, proxyAddrs, controlAddr, upstreamProxy, aiProvider, aiApiKey, aiModel, aiEndpoint, aiDisabled, oobEnabled, captureScopeOnly, suppressBrowserTelemetry, invisibleProxy, tlsBypassHosts, autoBypassOnPinFailure} — only fields present are changed"},
	{"GET", "/api/network/hosts", "List bindable network hosts with suggested LAN IP"},
	{"GET", "/api/proxy/device-endpoint", "Resolved device-facing proxy endpoint (auto/manual)"},
	{"POST", "/api/proxy/device-endpoint", "Set device proxy mode and optional manual host. Body: {mode, host?} — mode is auto|manual"},
	{"GET", "/api/sysproxy", "System-proxy status (supported/enabled)"},
	{"POST", "/api/sysproxy", "Enable/disable the OS system proxy (macOS). Body: {enabled}"},
	{"GET", "/api/android/status", "ADB availability, connected devices, and device proxy state"},
	{"POST", "/api/android/proxy", "Route a USB-connected Android device through Interseptor (adb reverse + global proxy). Body: {serial?, proxyMode?, wifiHost?} — proxyMode is usb (default) or wifi"},
	{"POST", "/api/android/unproxy", "Clear the Android device global proxy and adb reverse. Body: {serial?, removeSystemCA?}"},
	{"POST", "/api/android/install-ca", "Install the Interseptor CA on Android. Body: {serial?, mode?} — mode is user|system|auto (default user)"},
	{"POST", "/api/android/setup", "One-click Android setup: proxy + CA. Body: {serial?, proxyMode?, caMode?, wifiHost?} — proxyMode usb|wifi (default usb), caMode user|system|auto (default auto)"},
	{"GET", "/api/ios/status", "iOS simulators + USB devices, simctl/idevice availability, profile path"},
	{"GET", "/api/ios/profile.mobileconfig", "Configuration profile: Interseptor CA + global HTTP proxy (?host=&port=)"},
	{"POST", "/api/ios/setup", "One-click iOS setup: simctl CA + profile (simulator) or profile URL (device). Body: {udid?, proxyMode?, wifiHost?}"},
	{"POST", "/api/ios/install-ca", "Install CA on booted iOS Simulator via simctl. Body: {udid?}"},
	{"POST", "/api/ios/open-profile", "Open profile install URL in simulator Safari. Body: {udid?, target?}"},
	{"GET", "/api/ios/ssh/status", "Jailbroken iOS SSH readiness (TCP check via ?host=&port=)"},
	{"POST", "/api/ios/ssh/status", "Jailbroken iOS SSH auth check. Body: {host, port?, user, password?, keyPath?}"},
	{"POST", "/api/ios/ssh/setup", "Jailbroken iOS setup via SSH: open mobileconfig (CA + proxy) on device. Body: {host, port?, user, password?, keyPath?, proxyHost?, wifiHost?}"},
	{"POST", "/api/ios/ssh/install-ca", "Jailbroken iOS: open mobileconfig profile on device via SSH. Body: same shape as POST /api/ios/ssh/setup"},
	{"GET", "/api/session", "Get session/auth headers auto-applied to sends"},
	{"POST", "/api/session", "Set session/auth headers (auto-applied to Repeater/Intruder). Body: {enabled, headers?, unscoped?, macro?, loginMacro?, hostHeaders?} — headers is \"Key: Value\" lines; hostHeaders is {hostname: \"Key: Value\\n...\"} per-host overrides"},
	{"POST", "/api/session/login/run", "Run the login macro — refresh session headers from login response (no body)"},
	{"POST", "/api/session/login/test", "Dry-run the saved login macro without applying the live session; returns status + captured headers"},
	{"POST", "/api/session/login/from-flow/{id}", "Capture a flow's request as the login macro. Body: {enabled?, refreshSecs?, reauthOn401?}"},
	{"POST", "/api/session/auth", "Verify a submitted API key and set the browser session cookie; returns granted scope"},
	{"POST", "/api/session/logout", "Clear the browser session cookie"},
	{"GET", "/api/session/access-key", "Return the current browser session's API token (cookie-authed only) so the operator can copy it again from Settings → API Keys"},
	{"GET", "/login", "Login page (embedded HTML form) for remote/cookie-authed sessions"},
	{"GET", "/api/authz", "List saved authz test identities (roles)"},
	{"POST", "/api/authz", "Save authz identities (replaces the full list). Body: {identities: [{name, headers}]} — headers accepted as a \"Key: Value\" string, an array of such strings, or a {\"Key\":\"Value\"} object"},
	{"GET", "/api/authz/flow-auth/{id}", "Cookie/Authorization from a flow + Set-Cookie expiry hints"},
	{"POST", "/api/authz/from-flow/{id}", "Promote a flow's captured auth headers into a saved authz identity ({name, merge?})"},
	{"POST", "/api/authz/check-sessions", "Probe one flow as each identity — detect expired sessions. Body: {flowId}"},
	{"POST", "/api/authz/run", "Run authz test. Body: {flowId?, inScope?, maxFlows?, skipStatic?} — one of flowId/inScope required; maxFlows default 30 max 100. Response: {runs:[{flowId,method,host,path,baselineStatus,results}], summary:{endpoints,flagged}}"},
	{"POST", "/api/authz/cross-host-replay", "Replay a JWT-bearing endpoint to every unique in-scope host — detects cross-environment token confusion. Body: {flowId, jwtFlowId?, jwt?, mode?} — mode auto|bearer|path (default auto). Response: {flowId, method, path, mode, jwtSource, jwt (truncated preview), results:[{host,scheme,port,url,status,length,accepted,flowId}]}"},
	{"GET", "/api/readiness", "Aggregate pentest readiness checklist (proxy, traffic, scope, TLS interception, OOB, auth identities, login macro)"},
	{"GET", "/api/tls-diagnosis", "Diagnose whether HTTPS interception is working vs simply no traffic yet"},
	{"GET", "/api/flows/{id}/analyze", "Compact AI-friendly summary of a flow"},
	{"GET", "/api/flows/diff", "Diff two flows' responses (?a=&b=, optional maxBytes, format=text): status, length, headers, body"},
	{"PUT", "/api/flows/{id}/note", "Set or clear a flow note. Body: {note} — \"\" clears it"},
	{"PUT", "/api/flows/{id}/tags", "Replace a flow's tags. Body: {tags: []}"},
	{"POST", "/api/flows/tags", "Add or remove tags on many flows. Body: {flowIds: [], add?: [], remove?: []} — at least one of add/remove required"},
	{"GET", "/api/tags", "List tags in use with flow counts and colors"},
	{"PUT", "/api/tags/{tag}/color", "Set or clear a tag's display color. Body: {color} — hex like #4aa8ff, or \"\" to clear"},
	{"GET", "/api/endpoints", "Unique endpoints map (searchScope: path|headers|body|all)"},
	{"GET", "/api/notes", "Project markdown notebook"},
	{"PUT", "/api/notes", "Replace project notebook. Body: {notes} — markdown; inline data-URL images are extracted into SQLite on save"},
	{"PATCH", "/api/notes", "Atomically append a block to the project notebook. Body: {appendText}"},
	{"POST", "/api/notes/images", "Upload an image for the notebook. Body: {mime, data} — data is raw base64 or a data: URL. Response: {id} for a markdown image ref"},
	{"GET", "/api/notes/images/{id}", "Serve a notebook image"},
	{"GET", "/api/activity", "AI/MCP activity feed"},
	{"POST", "/api/activity", "Append an activity row (MCP stdio server)"},
	{"DELETE", "/api/activity", "Clear activity feed"},
	{"GET", "/api/project", "Active project + switch targets"},
	{"POST", "/api/project/switch", "Switch to another named project (re-exec). Body: {target} (plain project name) or {path} (absolute external folder) — mutually exclusive; target rejects path-like strings"},
	{"GET", "/api/oob/state", "OOB catcher state + interactions"},
	{"POST", "/api/oob/new", "Generate a new OOB callback token (no body). Response: {token, url}"},
	{"POST", "/api/oob/base", "Set public OOB base URL. Body: {baseUrl}"},
	{"DELETE", "/api/oob/interactions", "Clear OOB interaction log"},
	{"PUT", "/api/checks/disabled", "Disable/enable custom checks by id list. Body: {disabled: [ids]}"},
	{"GET", "/api/reference", "Machine-readable route catalog"},
	{"GET", "/api/mcp", "MCP tool descriptor + client config snippet"},
	{"GET", "/api/flows/{id}/curl", "Reconstruct the flow's request as a runnable curl command"},
	{"POST", "/api/ai/assist", "BYO-key AI: explain/suggest/summarize a flow. Body: {flowId?, flowIds?, findingId?, kind, question?, history?, agent?} — kind selects the assist mode; kind=\"ask\" uses question + history for follow-ups, agent:true lets the model send requests. Response: {text}"},
	{"POST", "/api/ai/notes/organize", "BYO-key AI: reorganize the project notebook. Body: {notes} — text to reorganize; empty falls back to the saved notebook. Response: {text}"},
	{"POST", "/api/ai/notes/organize/stream", "Streaming variant of notes organize. Body: same shape as POST /api/ai/notes/organize"},
	{"POST", "/api/ai/assist/stream", "Streaming variant of ai/assist (SSE, token-by-token). Body: same shape as POST /api/ai/assist"},
	{"POST", "/api/ai/findings/triage", "Ask AI to triage in-scope history and file evidence-backed findings (no active attacks). Body: {steer?}. SSE: status/tool/text/done/error"},
	{"POST", "/api/ai/actions", "BYO-key AI: suggested test payloads for a flow, formatted for one-click Repeater/Intruder loading"},
	{"GET", "/api/ai/openrouter/models", "OpenRouter model catalog (+ optional ?key= validation)"},
	{"GET", "/api/ai/providers", "List saved AI provider profiles (keys redacted) + the active profile id"},
	{"POST", "/api/ai/providers", "Create/update a saved AI provider profile ({id?,name,provider,apiKey?,model,endpoint})"},
	{"DELETE", "/api/ai/providers/{id}", "Delete a saved AI provider profile"},
	{"POST", "/api/ai/providers/{id}/activate", "Make a saved provider profile the active ai.* config"},
	{"GET", "/api/export/har", "Export history as HAR (optional ?inScope=1)"},
	{"POST", "/api/import/har", "Import a HAR file as flows. Body: raw HAR 1.2 JSON document (not wrapped). Response: {imported: n}"},
	{"GET", "/api/export/project", "Export a portable project (flows + rules + scope + settings)"},
	{"POST", "/api/import/project", "Import (merge) a project bundle. Body: {version, har, rules, scope, settings, notes?} — the JSON produced by GET /api/export/project. Response: {importedFlows, importedRules, importedScope}"},
	{"GET", "/api/export/full", "Download the active project as a lossless zip archive (DB + captured bodies)"},
	{"POST", "/api/import/full", "Upload a project zip and restore it as a new named project (?name=, ?overwrite=1)"},
	{"POST", "/api/export/full/file", "Write a full-project archive to a server-side path (for the local MCP agent)"},
	{"POST", "/api/import/full/file", "Restore a full-project archive from a server-side path into a new named project"},
	{"POST", "/api/merge/file", "Push receiver: ingest an uploaded project archive and merge it into the active project"},
	{"POST", "/api/merge/pull", "Download a peer's project archive over their share tunnel and merge it in ({peerUrl,key,label})"},
	{"POST", "/api/merge/push", "Build the active project's archive and push it to a peer's /api/merge/file ({peerUrl,key,label})"},
	{"GET", "/api/ca.crt", "Download the local CA certificate"},
	{"GET", "/api/keys", "List API keys"},
	{"POST", "/api/keys", "Create an API key. Body: {label?, scope?, expiresIn?} — scope full (default)|read; expiresIn is seconds from now, 0/omitted = never. Response: {token, key} — the token is returned exactly once"},
	{"DELETE", "/api/keys/{id}", "Revoke an API key"},
	{"GET", "/api/share/status", "Cloudflare quick-tunnel status (installed, running, public URL, whether an API key exists)"},
	{"POST", "/api/share/start", "Start a Cloudflare quick tunnel for remote access (refused without an API key)"},
	{"POST", "/api/share/stop", "Stop the share tunnel"},
	{"POST", "/mcp", "Streamable-HTTP MCP transport (JSON-RPC; for remote/hosted agents)"},
	{"GET", "/mcp", "Streamable-HTTP MCP transport — SSE stream for server-initiated messages"},
	{"OPTIONS", "/mcp", "CORS preflight for the Streamable-HTTP MCP transport"},
	{"GET", "/api/version", "Running version + whether a newer release is available"},
	{"GET", "/api/events", "Server-Sent Events stream of live updates"},
	{"POST", "/api/flows/delete", "Delete flows by id. Body: {ids: []}; content-addressed bodies are untouched (GC separately)"},
	{"POST", "/api/flows/purge", "Purge flows by host pattern; reclaims orphaned bodies in the background afterward (not reflected in this response). Body: {hosts: [], mode} — mode delete|keepOnly. Response: {deleted}"},
	{"POST", "/api/flows/gc", "Reclaim orphaned body files (no flows deleted, no body). Response: {removedFiles,freedBytes}"},
	{"GET", "/api/hosts/stats", "Per-host flow counts and byte totals, sorted desc by bytes. Response: {hosts:[{host,flows,bytes}],totalFlows,totalBytes}"},
	{"POST", "/api/human-input", "Register a human-input prompt and block up to 40s for the operator's answer ({message,options?})"},
	{"GET", "/api/human-input", "List pending human-input prompts raised by the AI"},
	{"GET", "/api/human-input/{id}", "Poll a human-input prompt for the operator's answer"},
	{"POST", "/api/human-input/{id}/respond", "Submit the operator's answer to a pending human-input prompt"},
	{"POST", "/api/autopwn/start", "Launch an autonomous, scope-gated pentest run ({budget,targetHint}). Response: {runId,state}"},
	{"POST", "/api/autopwn/stop", "Cancel the active autopwn run (kill switch)"},
	{"GET", "/api/autopwn/state", "Live snapshot of the current/last autopwn run"},
	{"GET", "/api/autopwn/runs", "Persisted autopwn run history, newest first"},
}

func (h *metaAPI) apiReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"baseUrl": "http://" + r.Host, "routes": apiRoutes})
}

// ---- MCP descriptor ----

var mcpDescriptor = map[string]any{
	"name":    "interseptor",
	"version": version.String(),
	"status":  "ready",
	"note":    "Run `interseptor` first. See GET /api/mcp for Cursor (HTTP /mcp) and stdio client configs.",
	"transport": map[string]any{
		"type":    "stdio",
		"command": "interseptor",
		"args":    []string{"mcp"},
	},
	// Alternative transport for hosted/remote agents that cannot spawn the
	// stdio subcommand: POST JSON-RPC to /mcp on this control port.
	"httpTransport": map[string]any{
		"type": "streamable-http",
		"url":  "/mcp",
		"note": "Stateless Streamable-HTTP MCP. POST a JSON-RPC message (or batch) to /mcp; no session id required. Same tools as stdio. Bind localhost-only.",
	},
	// Legacy default; apiMCP overwrites with mcpHTTPClientConfig(host) per request.
	"clientConfig": mcpHTTPClientConfig("http://127.0.0.1:9966"),
	"tools": []map[string]string{
		{"name": "list_flows", "desc": "List/search captured proxy flows"},
		{"name": "get_flow", "desc": "Read a flow's raw request/response"},
		{"name": "analyze_flow", "desc": "Compact summary: headers, params, scanner hits, scope"},
		{"name": "flow_as_curl", "desc": "Reconstruct a flow's request as a runnable curl command"},
		{"name": "diff_flows", "desc": "Diff two flows' responses: status, length, headers, body (baseline vs exploit)"},
		{"name": "set_note", "desc": "Annotate a flow with a note (\"\" clears it)"},
		{"name": "get_notes", "desc": "Read the project's shared markdown notebook"},
		{"name": "set_notes", "desc": "Replace the project's shared markdown notebook"},
		{"name": "append_notes", "desc": "Append a markdown block to the project notebook"},
		{"name": "tag_flow", "desc": "Attach tags to a flow for triage/grouping"},
		{"name": "untag_flow", "desc": "Remove tags from a flow (others kept)"},
		{"name": "list_tags", "desc": "List tags in use with flow counts"},
		{"name": "create_finding", "desc": "Record a structured vulnerability finding with description + impact (durable memory)"},
		{"name": "list_findings", "desc": "List findings (with PoC flows), filter by severity/status"},
		{"name": "update_finding", "desc": "Update a finding's status, impact, or any field (e.g. mark verified)"},
		{"name": "add_finding_poc", "desc": "Attach a request/response flow to a finding as PoC evidence"},
		{"name": "add_finding_image", "desc": "Attach a screenshot/image (base64) to a finding as evidence"},
		{"name": "remove_finding_poc", "desc": "Detach a PoC flow from a finding"},
		{"name": "delete_finding", "desc": "Permanently delete a finding (cannot be undone)"},
		{"name": "export_report", "desc": "Engagement report (curated findings + PoCs; passive scan omitted unless includeIssues=true). format=html optional"},
		{"name": "export_full_project", "desc": "Write a lossless portable archive of the whole project (DB + captured bodies) to a server-side .zip path"},
		{"name": "import_full_project", "desc": "Restore a full-project .zip archive into a new named project under ~/.interseptor/projects"},
		{"name": "send_request", "desc": "Replay/mutate a request (Repeater)"},
		{"name": "start_intruder", "desc": "Run Sniper/Battering/Pitchfork/Cluster payload attack"},
		{"name": "intruder_state", "desc": "Attack progress + results"},
		{"name": "run_scanner", "desc": "Passive scan of captured flows"},
		{"name": "list_issues", "desc": "Scanner findings"},
		{"name": "scan_report", "desc": "Findings as a Markdown report (grouped by severity)"},
		{"name": "active_scan", "desc": "Active scan (sends attack payloads; arm to consent)"},
		{"name": "active_scan_state", "desc": "Active-scan progress + findings"},
		{"name": "active_scan_stop", "desc": "Stop the running active scan"},
		{"name": "autopwn_start", "desc": "Launch a fully-autonomous, scope-gated, verified-only pentest run"},
		{"name": "autopwn_state", "desc": "Autonomous-pentest run progress + counts"},
		{"name": "autopwn_stop", "desc": "Stop the running autonomous pentest (kill switch)"},
		{"name": "list_checks", "desc": "List custom Starlark scanner checks"},
		{"name": "test_check", "desc": "Compile + run a check against a flow (no save)"},
		{"name": "save_check", "desc": "Create/update a validated custom scanner check"},
		{"name": "delete_check", "desc": "Delete a custom scanner check"},
		{"name": "get_intercept", "desc": "Intercept state + hold queue"},
		{"name": "set_intercept", "desc": "Toggle request interception"},
		{"name": "set_response_intercept", "desc": "Toggle response interception"},
		{"name": "forward_request", "desc": "Forward a held request (optionally edited)"},
		{"name": "drop_request", "desc": "Drop a held request"},
		{"name": "list_rules", "desc": "List match-&-replace rules"},
		{"name": "add_rule", "desc": "Add a request match-&-replace rule"},
		{"name": "list_ws_frames", "desc": "WebSocket frames for a flow"},
		{"name": "ws_send", "desc": "WebSocket Repeater: open a socket, send a message, read replies"},
		{"name": "list_scope", "desc": "List target-scope rules"},
		{"name": "add_scope_rule", "desc": "Add an in/out-of-scope rule"},
		{"name": "scope_from_url", "desc": "Add a target URL's host/scheme to scope (self-scope)"},
		{"name": "check_readiness", "desc": "Structured pre-flight checklist (proxy, scope, traffic, tls_intercept, OOB, auth identities, login macro)"},
		{"name": "detect_ssl_pinning", "desc": "Diagnose SSL pinning / untrusted CA vs no traffic (mobile pentest)"},
		{"name": "request_human_input", "desc": "Pause and ask the human a question (handoff gate)"},
		{"name": "get_human_response", "desc": "Retrieve the human's answer to a request_human_input"},
		{"name": "get_settings", "desc": "Proxy/intercept settings"},
		{"name": "set_session", "desc": "Auth headers auto-applied to every send (keeps requests authenticated)"},
		{"name": "run_login_macro", "desc": "Run login macro — refresh session from login response"},
		{"name": "get_authz", "desc": "List authz test identities (roles)"},
		{"name": "set_authz", "desc": "Save authz identities"},
		{"name": "authz_run", "desc": "Run authorization test (flowId or inScope:true)"},
		{"name": "authz_check_sessions", "desc": "Probe session validity per identity on one flow"},
		{"name": "cross_host_token_replay", "desc": "Replay endpoint to all in-scope hosts with a JWT — detects cross-env token confusion"},
		{"name": "oob_state", "desc": "OOB blind-callback catcher state + hits"},
		{"name": "oob_new", "desc": "Generate a new OOB callback URL/token"},
		{"name": "oob_set_base", "desc": "Set the public OOB base URL (ngrok/VPS/LAN)"},
		{"name": "oob_enable", "desc": "Enable the OOB interaction catcher (one-click)"},
		{"name": "get_flow_auth", "desc": "Extract Cookie/Authorization/XSRF from a flow for auth setup"},
		{"name": "promote_flow_to_authz", "desc": "Promote a flow's auth headers into an authz identity"},
		{"name": "set_login_macro_from_flow", "desc": "Capture a flow as the login macro (CSRF/session refresh)"},
		{"name": "set_login_macro", "desc": "Configure login macro (raw HTTP + target URL)"},
		{"name": "test_login_macro", "desc": "Dry-run login macro without applying session"},
		{"name": "decode", "desc": "Decode/encode (base64, url, hex, html, jwt, smart)"},
		{"name": "ca_info", "desc": "How to trust the CA for HTTPS"},
		{"name": "android_status", "desc": "ADB devices, LAN host, and device proxy state"},
		{"name": "android_setup", "desc": "One-click Android intercept setup (proxy + CA via adb)"},
		{"name": "android_teardown", "desc": "Clear Android proxy and optionally remove system CA"},
		{"name": "ios_status", "desc": "iOS simulators/devices + profile path"},
		{"name": "ios_setup", "desc": "One-click iOS intercept (simulator simctl + mobileconfig profile)"},
		{"name": "ios_install_ca", "desc": "Install CA on iOS Simulator via simctl"},
		{"name": "ios_ssh_status", "desc": "Jailbroken iOS SSH reachability and auth check"},
		{"name": "ios_ssh_setup", "desc": "Jailbroken iOS intercept setup via SSH (profile CA + proxy)"},
		{"name": "ios_ssh_install_ca", "desc": "Jailbroken iOS: open mobileconfig on device via SSH"},
		{"name": "host_stats", "desc": "Per-host flow/byte breakdown — use before prune_history"},
		{"name": "prune_history", "desc": "DESTRUCTIVE: delete flows by host pattern (delete noisy hosts or keepOnly important ones)"},
	},
}

func (h *metaAPI) apiMCP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpDescriptorForRequest(r.Host))
}
