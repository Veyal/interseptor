package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/version"
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
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Label == "" {
		in.Label = "key"
	}
	token, key, err := h.st.CreateAPIKey(in.Label)
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
	{"GET", "/api/flows", "List captured proxy flows (filters: method, host, search, searchScope=body, hasNote=1, scheme, status, before, limit)"},
	{"GET", "/api/flows/{id}", "Flow detail (headers, body hashes, flags)"},
	{"GET", "/api/flows/{id}/raw", "Reconstructed raw request/response (?side=req|res)"},
	{"GET", "/api/flows/{id}/body", "Body bytes only (?side=req|res) — for download with MIME extension"},
	{"GET", "/api/flows/{id}/ws", "Captured WebSocket frames for a flow"},
	{"GET", "/api/flows/inscope", "Whether any in-scope traffic exists (paginated; for readiness checks)"},
	{"GET", "/api/params", "Aggregate query/form/JSON parameter names from captured traffic (?host=, ?inScope=1)"},
	{"POST", "/api/ws/send", "WebSocket Repeater: open a socket, send a message, return reply frames"},
	{"POST", "/api/decode", "Decode/encode a string (base64, url, hex, html, jwt, smart)"},
	{"GET", "/api/rules", "List match-&-replace rules"},
	{"POST", "/api/rules", "Create a rule"},
	{"PUT", "/api/rules/{id}", "Update a rule"},
	{"DELETE", "/api/rules/{id}", "Delete a rule"},
	{"GET", "/api/intercept", "Intercept state + hold queue"},
	{"POST", "/api/intercept/toggle", "Enable/disable intercept"},
	{"POST", "/api/intercept/{id}/forward", "Forward a held request (optionally edited)"},
	{"POST", "/api/intercept/{id}/drop", "Drop a held request"},
	{"POST", "/api/repeater/send", "Send a request from Repeater"},
	{"GET", "/api/repeater/history", "Repeater send history"},
	{"POST", "/api/intruder/start", "Start a Sniper/Pitchfork attack"},
	{"GET", "/api/intruder/state", "Current attack progress + results"},
	{"POST", "/api/scanner/run", "Run passive checks over captured flows"},
	{"GET", "/api/scanner/issues", "List scanner findings"},
	{"GET", "/api/scanner/report", "Download scanner findings as a Markdown report"},
	{"GET", "/api/activescan", "Active-scan state (armed/running/findings/probe log)"},
	{"GET", "/api/activescan/history", "Active-scan probe history (all FlagActiveScan flows)"},
	{"POST", "/api/activescan/arm", "Arm/disarm active scanning (consent gate)"},
	{"POST", "/api/activescan/start", "Start an active scan (sends attack payloads; flowId or inScope)"},
	{"POST", "/api/activescan/stop", "Stop the running active scan"},
	{"GET", "/api/checks", "List custom Starlark scanner checks (id, source, compile error)"},
	{"GET", "/api/checks/reference", "Custom-check authoring reference (Starlark API, markdown)"},
	{"POST", "/api/checks/test", "Compile + run a check without saving"},
	{"POST", "/api/ai/checks/generate", "BYO-key AI: plain-text description → Starlark check source"},
	{"GET", "/api/checks/{id}", "Read a custom check's source"},
	{"PUT", "/api/checks/{id}", "Create/update a custom check (rejected if it doesn't compile)"},
	{"DELETE", "/api/checks/{id}", "Delete a custom check"},
	{"GET", "/api/views", "List saved history views"},
	{"POST", "/api/views", "Save the current filters as a named view"},
	{"DELETE", "/api/views/{id}", "Delete a saved view"},
	{"GET", "/api/scope", "List target-scope rules"},
	{"POST", "/api/scope", "Add a scope rule (include/exclude)"},
	{"PUT", "/api/scope/{id}", "Update a scope rule"},
	{"DELETE", "/api/scope/{id}", "Delete a scope rule"},
	{"GET", "/api/settings", "Get proxy/intercept settings"},
	{"PUT", "/api/settings", "Update settings (rebinds proxy/control listeners)"},
	{"GET", "/api/network/hosts", "List bindable network hosts with suggested LAN IP"},
	{"GET", "/api/proxy/device-endpoint", "Resolved device-facing proxy endpoint (auto/manual)"},
	{"POST", "/api/proxy/device-endpoint", "Set device proxy mode (auto|manual) and optional manual host"},
	{"GET", "/api/sysproxy", "System-proxy status (supported/enabled)"},
	{"POST", "/api/sysproxy", "Enable/disable the OS system proxy (macOS)"},
	{"GET", "/api/android/status", "ADB availability, connected devices, and device proxy state"},
	{"POST", "/api/android/proxy", "Route a USB-connected Android device through Interceptor (adb reverse + global proxy)"},
	{"POST", "/api/android/unproxy", "Clear the Android device global proxy and adb reverse"},
	{"POST", "/api/android/install-ca", "Install the Interceptor CA on Android ({mode:user|system|auto})"},
	{"POST", "/api/android/setup", "One-click Android setup: proxy + CA ({proxyMode:usb|wifi, caMode:user|system|auto})"},
	{"GET", "/api/ios/status", "iOS simulators + USB devices, simctl/idevice availability, profile path"},
	{"GET", "/api/ios/profile.mobileconfig", "Configuration profile: Interceptor CA + global HTTP proxy (?host=&port=)"},
	{"POST", "/api/ios/setup", "One-click iOS setup: simctl CA + profile (simulator) or profile URL (device)"},
	{"POST", "/api/ios/install-ca", "Install CA on booted iOS Simulator via simctl"},
	{"POST", "/api/ios/open-profile", "Open profile install URL in simulator Safari"},
	{"GET", "/api/ios/ssh/status", "Jailbroken iOS SSH readiness (TCP check via ?host=&port=)"},
	{"POST", "/api/ios/ssh/status", "Jailbroken iOS SSH auth check ({host, user, password?, keyPath?})"},
	{"POST", "/api/ios/ssh/setup", "Jailbroken iOS setup via SSH: open mobileconfig (CA + proxy) on device"},
	{"POST", "/api/ios/ssh/install-ca", "Jailbroken iOS: open mobileconfig profile on device via SSH"},
	{"GET", "/api/session", "Get session/auth headers auto-applied to sends"},
	{"POST", "/api/session", "Set session/auth headers (auto-applied to Repeater/Intruder)"},
	{"POST", "/api/session/login/run", "Run the login macro — refresh session headers from login response"},
	{"POST", "/api/session/login/from-flow/{id}", "Capture a flow's request as the login macro"},
	{"GET", "/api/authz", "List saved authz test identities (roles)"},
	{"POST", "/api/authz", "Save authz identities"},
	{"GET", "/api/authz/flow-auth/{id}", "Cookie/Authorization from a flow + Set-Cookie expiry hints"},
	{"POST", "/api/authz/check-sessions", "Probe one flow as each identity — detect expired sessions"},
	{"POST", "/api/authz/run", "Run authz test (flowId or inScope:true, maxFlows cap)"},
	{"POST", "/api/discovery/start", "Start content discovery (forced-browse) against a base URL"},
	{"POST", "/api/discovery/stop", "Stop the running discovery scan"},
	{"GET", "/api/discovery/state", "Discovery run progress + results"},
	{"GET", "/api/discovery/wordlist", "Built-in default discovery wordlist (plain text)"},
	{"GET", "/api/discovery/seeds", "Path segments from captured history for a host (?host=)"},
	{"GET", "/api/discovery/suggest", "Merged history seeds + optional AI path suggestions"},
	{"GET", "/api/discovery/scope-targets", "Base URLs derived from in-scope include rules"},
	{"POST", "/api/discovery/inspect", "Re-send one discovered URL and return its flow id for inspect"},
	{"GET", "/api/flows/{id}/analyze", "Compact AI-friendly summary of a flow"},
	{"GET", "/api/flows/diff", "Diff two flows' responses (?a=&b=, optional maxBytes, format=text): status, length, headers, body"},
	{"PUT", "/api/flows/{id}/note", "Set or clear a flow note"},
	{"PUT", "/api/flows/{id}/tags", "Replace a flow's tags"},
	{"POST", "/api/flows/tags", "Add or remove tags on many flows (selection)"},
	{"GET", "/api/tags", "List tags in use with flow counts and colors"},
	{"PUT", "/api/tags/{tag}/color", "Set or clear a tag's display color"},
	{"GET", "/api/endpoints", "Unique endpoints map (searchScope: path|headers|body|all)"},
	{"GET", "/api/notes", "Project markdown notebook"},
	{"PUT", "/api/notes", "Replace project notebook"},
	{"POST", "/api/notes/images", "Upload an image for the notebook"},
	{"GET", "/api/notes/images/{id}", "Serve a notebook image"},
	{"GET", "/api/activity", "AI/MCP activity feed"},
	{"POST", "/api/activity", "Append an activity row (MCP stdio server)"},
	{"DELETE", "/api/activity", "Clear activity feed"},
	{"GET", "/api/project", "Active project + switch targets"},
	{"POST", "/api/project/switch", "Switch to another named project (re-exec)"},
	{"GET", "/api/oob/state", "OOB catcher state + interactions"},
	{"POST", "/api/oob/new", "Generate a new OOB callback token"},
	{"POST", "/api/oob/base", "Set public OOB base URL"},
	{"DELETE", "/api/oob/interactions", "Clear OOB interaction log"},
	{"PUT", "/api/checks/disabled", "Disable/enable custom checks by id list"},
	{"GET", "/api/reference", "Machine-readable route catalog"},
	{"GET", "/api/mcp", "MCP tool descriptor + client config snippet"},
	{"GET", "/api/flows/{id}/curl", "Reconstruct the flow's request as a runnable curl command"},
	{"POST", "/api/ai/assist", "BYO-key AI: explain/suggest/summarize a flow"},
	{"POST", "/api/ai/notes/organize", "BYO-key AI: reorganize the project notebook"},
	{"POST", "/api/ai/notes/organize/stream", "Streaming variant of notes organize"},
	{"GET", "/api/ai/openrouter/models", "OpenRouter model catalog (+ optional ?key= validation)"},
	{"GET", "/api/export/har", "Export history as HAR (optional ?inScope=1)"},
	{"POST", "/api/import/har", "Import a HAR file as flows"},
	{"GET", "/api/export/project", "Export a portable project (flows + rules + scope + settings)"},
	{"POST", "/api/import/project", "Import (merge) a project bundle"},
	{"GET", "/api/ca.crt", "Download the local CA certificate"},
	{"GET", "/api/keys", "List API keys"},
	{"POST", "/api/keys", "Create an API key"},
	{"DELETE", "/api/keys/{id}", "Revoke an API key"},
	{"POST", "/mcp", "Streamable-HTTP MCP transport (JSON-RPC; for remote/hosted agents)"},
	{"GET", "/api/version", "Running version + whether a newer release is available"},
	{"GET", "/api/events", "Server-Sent Events stream of live updates"},
	{"POST", "/api/flows/purge", "Purge flows by host pattern ({hosts:[],mode:delete|keepOnly}); runs GC. Response: {deleted,removedFiles,freedBytes}"},
	{"POST", "/api/flows/gc", "Reclaim orphaned body files (no flows deleted). Response: {removedFiles,freedBytes}"},
	{"GET", "/api/hosts/stats", "Per-host flow counts and byte totals, sorted desc by bytes. Response: {hosts:[{host,flows,bytes}],totalFlows,totalBytes}"},
}

func (h *metaAPI) apiReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"baseUrl": "http://" + r.Host, "routes": apiRoutes})
}

// ---- MCP descriptor ----

var mcpDescriptor = map[string]any{
	"name":    "interceptor",
	"version": version.Version,
	"status":  "ready",
	"note":    "Run `interceptor` first. See GET /api/mcp for Cursor (HTTP /mcp) and stdio client configs.",
	"transport": map[string]any{
		"type":    "stdio",
		"command": "interceptor",
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
		{"name": "remove_finding_poc", "desc": "Detach a PoC flow from a finding"},
		{"name": "export_report", "desc": "Engagement report (curated findings + PoCs; passive scan omitted unless includeIssues=true). format=html optional"},
		{"name": "export_full_project", "desc": "Write a lossless portable archive of the whole project (DB + captured bodies) to a server-side .zip path"},
		{"name": "import_full_project", "desc": "Restore a full-project .zip archive into a new named project under ~/.interceptor/projects"},
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
		{"name": "get_discovery_wordlist", "desc": "Built-in default content-discovery wordlist"},
		{"name": "start_discovery", "desc": "Forced-browse paths from a wordlist (dirbuster-style)"},
		{"name": "discovery_state", "desc": "Discovery scan progress + found paths"},
		{"name": "stop_discovery", "desc": "Stop the running discovery scan"},
		{"name": "suggest_discovery_paths", "desc": "Path suggestions from history + optional AI"},
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
