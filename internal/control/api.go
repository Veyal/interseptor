package control

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/version"
)

const (
	maxAPIKeyRequestBytes = 4 << 10
	maxAPIKeyLabelBytes   = 256
)

// ---- API keys ----

func (h *metaAPI) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.st.ListAPIKeys()
	if err != nil {
		httpInternalErr(w, err)
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
	if !decodeOptionalLimitedJSON(w, r, maxAPIKeyRequestBytes, &in) {
		return
	}
	if len(in.Label) > maxAPIKeyLabelBytes {
		httpErr(w, http.StatusBadRequest, "label too long")
		return
	}
	if in.Scope != "" && in.Scope != store.ScopeFull && in.Scope != store.ScopeRead {
		httpErr(w, http.StatusBadRequest, "scope must be full or read")
		return
	}
	if in.ExpiresIn < 0 {
		httpErr(w, http.StatusBadRequest, "expiresIn must not be negative")
		return
	}
	if in.Label == "" {
		in.Label = "key"
	}
	var expires int64
	if in.ExpiresIn > 0 {
		now := time.Now().UnixMilli()
		if in.ExpiresIn > (math.MaxInt64-now)/1000 {
			httpErr(w, http.StatusBadRequest, "expiresIn is too large")
			return
		}
		expires = now + in.ExpiresIn*1000
	}
	token, key, err := h.st.CreateAPIKey(in.Label, store.NormalizeScope(in.Scope), expires)
	if err != nil {
		httpInternalErr(w, err)
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
		httpInternalErr(w, err)
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
	{"GET", "/api/findings/readiness", "Project-wide report readiness: board rows (id, title, severity, status, ready, blocking gaps) sorted by severity, per-finding checks and final-gate issues {rule, field, capability, message}; filters statuses and tag"},
	{"GET", "/api/finding-quality/{id}", "Final report-quality gate for one finding; same issues as the project-wide readiness entry"},
	{"GET", "/api/findings/deleted", "List recoverable deleted findings"},
	{"GET", "/api/finding-revisions/{id}", "List immutable revision metadata; ?before= for pagination"},
	{"GET", "/api/finding-revisions/{id}/{revisionId}", "Historical snapshot and field-level diff"},
	{"POST", "/api/finding-revisions/{id}/{revisionId}/restore", "Restore a version as a new revision; body {reason?}"},
	{"POST", "/api/finding-targets/preview", "Preview deduplication and optional templates; body {targets?,legacy?}; no persistence"},
	{"POST", "/api/findings/{id}/normalize-targets", "Apply reviewer-approved path templates to a finding's targets; body {approve:[suggestion indexes], dryRun?} (dryRun defaults to true; false persists)"},
	{"GET", "/api/flows", "List compact captured proxy flows as {flows:[{id,method,host,path,...}],truncated}; filters: method, host, search, searchScope=anywhere|body|id, savedSearch, hasNote=1, scheme, status, before, limit, inScope=1, includeTools=1. By default excludes Repeater/Intruder (History-shaped); includeTools=1 returns all sources"},
	{"GET", "/api/flows/{id}/auth-timeline", "Read-only auth timeline for a login flow and the same client's following flows (windowSeconds default 120 max 600, max default 40): redirects, Set-Cookie create/replace/clear/reject, cookie send/omit, session and CSRF rotation, MFA state, scheme/host changes, and the first transition where authenticated state appears lost. Inferences are labelled hypothesis; cookie values are never returned (short fingerprints only)"},
	{"GET", "/api/flows/session-inspect", "Passive session timeline for selected captured flow ids (ids=1,2&roles=anonymous,user); cookie values are redacted, fingerprints are short, and browser decisions/MFA completion/side effects remain unknown"},
	{"GET", "/api/flow-searches", "List project-scoped saved flow searches without source"},
	{"POST", "/api/flow-searches", "Compile and save a project-scoped Starlark flow search. Body: {name,scope,script}"},
	{"POST", "/api/flow-searches/test", "Compile a Starlark flow search without saving. Body: {name,scope,script}"},
	{"GET", "/api/flow-searches/{name}/source", "Get source for one project-scoped saved flow search"},
	{"PUT", "/api/flow-searches/{name}", "Compile and replace a project-scoped saved flow search. Body: {name?,scope,script}"},
	{"DELETE", "/api/flow-searches/{name}", "Delete one project-scoped saved flow search"},
	{"GET", "/api/flows/{id}", "Flow detail (headers, body hashes, flags)"},
	{"GET", "/api/flows/{id}/raw", "Reconstructed raw request/response (?side=req|res)"},
	{"GET", "/api/flows/{id}/decoded", "App-layer message codec decode (?side=req|res). Response: {matched, codecId, plaintext, fields?, applyOnSend?, error?} — display-only; never mutates the stored flow"},
	{"GET", "/api/flows/{id}/preview.png", "Interseptor-styled PNG preview of request/response (?side=both|req|res, pretty=0|1 default 1, layout=horizontal|vertical default horizontal with request left / response right, theme=light|dark default light)"},
	{"GET", "/api/flows/{id}/body", "Body bytes only (?side=req|res) — for download with MIME extension"},
	{"GET", "/api/flows/{id}/ws", "Captured WebSocket frames for a flow"},
	{"GET", "/api/flows/inscope", "Boolean readiness probe: {inScope}; use GET /api/flows?inScope=1 for compact flow rows"},
	{"GET", "/api/params", "Aggregate query/form/JSON parameter names from captured traffic (?host=, ?inScope=1)"},
	{"POST", "/api/ws/send", "WebSocket Repeater: open a socket, send a message, return reply frames. Body: {url, message, binary?, headers?} — headers is \"Key: Value\" lines. Response: {status, frames, flowId} — the handshake and every frame are recorded as a flow (flowId) that findings can cite; a failed handshake returns {error, status, flowId} with 502"},
	{"PUT", "/api/flows/{id}/ws/{frameId}/note", "Annotate one captured WebSocket frame. Body: {note} — \"\" clears. 204 on success, 404 if the frame is not part of the flow"},
	{"POST", "/api/decode", "Decode/encode a string (base64, url, hex, html, jwt, smart). Body: {op, input} — op one of base64encode/base64decode/urlencode/urldecode/hexencode/hexdecode/htmlencode/htmldecode/jwtdecode/smart. Response: {output} or {error} (200 either way)"},
	{"POST", "/api/selection-decode", "Preview decode for highlighted text: project message codecs (when flowId given) then smart. Body: {input, flowId?, side?}. Response: {matched, kind, output, codecId?, title?, note?} or {matched:false}"},
	{"GET", "/api/rules", "List match-&-replace rules"},
	{"POST", "/api/rules", "Create a rule. Body: {ord, enabled, type, match, replace, bigBody?} — type one of req-header/req-body/res-header/res-body; match is a regex; bigBody opts a body rule into the 64 MB scan cap (default 2 MB)"},
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
	{"POST", "/api/repeater/send", "Send a request from Repeater. Body: {method, url, headers?, body?, bodyMode?, codecId?, flowId?, rawBody?} — headers is \"Key: Value\" lines or a {\"Key\":\"Value\"} object; bodyMode=decoded + codecId re-encodes plaintext via a message codec before send; refuses targets that resolve to Interseptor's own listeners. Response: flow detail (id, status, headers, body hashes)"},
	{"GET", "/api/repeater/history", "Repeater send history"},
	{"POST", "/api/flows/{id}/replay", "Re-send a captured flow's request as a new Repeater flow. Body: {session} — \"current\" applies the active session headers, \"flow\" (default) replays exactly as captured."},
	{"GET", "/replay/{id}", "Side-effect-free replay confirmation page (?session=current|flow) that POSTs to /api/flows/{id}/replay only after operator confirmation."},
	{"POST", "/api/intruder/start", "Start a Sniper/Battering/Pitchfork/Cluster attack. Body: {target, template, attackType, payloads, repeat?, threads?, delayMs?, grepMatch?, grepExtract?, processRules?} — template marks fuzz points with §…§, payloads is a list of payload lists. Response: attack state"},
	{"POST", "/api/intruder/stop", "Stop an active Intruder attack"},
	{"GET", "/api/intruder/state", "Current attack progress + results"},
	{"POST", "/api/scanner/run", "Run passive checks over captured flows (no body)"},
	{"GET", "/api/scanner/targets", "List every distinct in-scope scanner host as {hosts:[{host,count}],truncated:false}; exhaustively paginates captured flows"},
	{"GET", "/api/scanner/issues", "List scanner findings"},
	{"DELETE", "/api/scanner/issues", "Clear passive scanner issues only; curated Findings are unchanged"},
	{"GET", "/api/scanner/report", "Download scanner findings as a Markdown report"},
	{"GET", "/api/checks", "List custom Starlark scanner checks (id, source, compile error)"},
	{"GET", "/api/checks/reference", "Custom-check authoring reference (Starlark API, markdown)"},
	{"POST", "/api/checks/test", "Compile + run a check without saving. Body: {source, flowId?} — flowId omitted uses the most recent captured flow. Response: {findings} or {error}"},
	{"GET", "/api/checks/{id}", "Read a custom check's source"},
	{"PUT", "/api/checks/{id}", "Create/update a custom check (rejected if it doesn't compile). Body: {source}"},
	{"DELETE", "/api/checks/{id}", "Delete a custom check"},
	{"GET", "/api/codecs", "List project-scoped Starlark message codecs (id, source, meta, compile error)"},
	{"GET", "/api/codecs/reference", "Message-codec authoring reference (Starlark API, markdown)"},
	{"POST", "/api/codecs/test", "Compile + match/decode a codec against a flow without saving. Body: {source?, id?, flowId?, side?}"},
	{"POST", "/api/codecs/encode", "Re-encode edited plaintext to a wire body. Body: {source?, id?, flowId?, side?, plaintext, rawBody?}"},
	{"GET", "/api/codecs/{id}", "Read a project message codec's source + meta"},
	{"PUT", "/api/codecs/{id}", "Create/update a project message codec (must compile). Body: {source}"},
	{"DELETE", "/api/codecs/{id}", "Delete a project message codec"},
	{"GET", "/api/packs", "List installed rule packs (name, version, check ids)"},
	{"GET", "/api/packs/{name}", "Show one installed pack's record"},
	{"POST", "/api/packs/install", "Install a rule-pack .tar.gz (sha256 + ed25519 signature; ?allowUnsigned=1 to skip sig); full-scope only"},
	{"DELETE", "/api/packs/{name}", "Uninstall a rule pack and delete its check files; full-scope only"},
	{"GET", "/api/findings", "List curated findings (optional ?severity=&status=&tag=; view=summary returns a bounded lightweight projection)"},
	{"GET", "/api/findings/tags", "List tags in use on findings with counts (and optional colors from tag_meta)"},
	{"GET", "/api/findings/report", "Curated findings as Markdown/HTML/JSON (?format=html|json; ?tag=; ?groupBy=tag; ?omitTags=; ?tagOrder=; ?issues=1; ?includeBodies=0; ?audit=1 appends the revision audit trail without values)"},
	{"POST", "/api/findings", "Create an evidence-first finding. Body: {title, summary?, severity?, status?, confidence?, target?, targets?, proofReview?, claims?, notExecuted?, relatedFindings?, impact?, why?, cwe?, environment?, fix?, retest?, cvss?, verificationInstructions?, blocks?|body?, flowIds?, tags?, detail?, evidence?} — title required (stub OK); send blocks or legacy body, not both"},
	{"GET", "/api/findings/{id}", "Get one canonical finding with typed blocks, proof/provenance, PoC flows, tags, structured readiness, and legacy ready/missing compatibility"},
	{"PATCH", "/api/findings/{id}", "Update only sent finding fields. Accepts summary/confidence/retest, ordered targets, proofReview, claims (per-claim verdicts), notExecuted, relatedFindings, CVSS v4 vector, and structured blocks; send blocks or legacy body, not both"},
	{"DELETE", "/api/findings/{id}", "Permanently delete a finding"},
	{"POST", "/api/findings/{id}/flows", "Attach a captured flow as evidence. Body: {flowId, role?, note?, proof?, position?}; role identifies reproduction purpose and proof states exactly what the flow establishes"},
	{"DELETE", "/api/findings/{id}/flows/{flowId}", "Detach a PoC flow from a finding"},
	{"POST", "/api/findings/{id}/images", "Validate, store, and atomically attach screenshot evidence. Body: {data, mime?, caption?, role?, proof?, source?, sourceFlowId?, position?}; max 5 MiB"},
	{"POST", "/api/findings/{id}/images/{hash}/classify", "Reviewer relabel of an attached image without re-upload. Body: {source: browser_screenshot|device_screenshot|operator_upload|tool_output|other, reason?}; ingestion metadata is kept, the classifier is recorded, generated previews cannot be relabelled"},
	{"POST", "/api/findings/{id}/flow-preview", "Render and atomically attach a labeled HTTP PNG with source=flow_preview and sourceFlowId. Body: {flowId, side?, pretty?, layout?, theme?, caption?, role?, proof?, position?}"},
	{"GET", "/api/findings/images/{hash}", "Serve a content-addressed finding image by sha256 hash"},
	{"GET", "/api/views", "List saved history views"},
	{"POST", "/api/views", "Save the current filters as a named view. Body: {name, data} — data is an arbitrary JSON filter-state blob"},
	{"DELETE", "/api/views/{id}", "Delete a saved view"},
	{"GET", "/api/scope", "List target-scope rules"},
	{"POST", "/api/scope", "Add a scope rule. Body: {action, host?, path?, scheme?, port?} — action is include|exclude; needs at least one of host/path/scheme/port"},
	{"PUT", "/api/scope/{id}", "Update a scope rule. Body: same shape as POST /api/scope"},
	{"DELETE", "/api/scope/{id}", "Delete a scope rule"},
	{"GET", "/api/settings", "Get proxy/intercept settings"},
	{"PUT", "/api/settings", "Update settings (rebinds proxy/control listeners). Body: any subset of {proxyAddr, proxyAddrs, controlAddr, upstreamProxy, upstreamProxyCA, oobEnabled, captureScopeOnly, suppressBrowserTelemetry, suppressAndroidTelemetry, invisibleProxy, originTLSVerify, tlsBypassHosts, originTLSVerifyBypassHosts, autoBypassOnPinFailure, proxyAuthEnabled, proxyAuthUser, proxyAuthPassword} — only fields present are changed; proxy auth is optional listener basic auth (default off) and is not an API key; suppressBrowserTelemetry is the compatibility name for known browser-background capture suppression"},
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
	{"POST", "/api/authz", "Save authz identities. Body: {identities: [{name, headers}], mode?: merge|replace (default merge by name — other identities are kept), owner?} — headers accepted as a \"Key: Value\" string, an array of such strings, or a {\"Key\":\"Value\"} object"},
	{"POST", "/api/authz/identity", "Add or update one authz identity by name without touching others. Body: {name, headers, owner?}"},
	{"DELETE", "/api/authz/identity/{name}", "Remove one authz identity by name (404 if absent)"},
	{"GET", "/api/authz/flow-auth/{id}", "Cookie/Authorization from a flow + Set-Cookie expiry hints"},
	{"POST", "/api/authz/from-flow/{id}", "Promote a flow's captured auth headers into a saved authz identity ({name, merge?})"},
	{"POST", "/api/authz/check-sessions", "Probe one flow as each identity — detect expired sessions. Body: {flowId}"},
	{"POST", "/api/authz/run", "Run authz test. Body: {flowId?, inScope?, maxFlows?, skipStatic?} — one of flowId/inScope required; maxFlows default 30 max 100. Response: {runs:[{flowId,method,host,path,baselineStatus,results}], summary:{endpoints,flagged}}"},
	{"POST", "/api/authz/differential", "Differential auth test: replay ONE flow as anonymous + selected identities and classify each as auth_failure | authz_failure | validation_failure | success. Body: {flowId, identities?: [names], invalidBody?: string (probes whether auth runs before validation), sideEffectFlowId?: read-only state flow replayed before/after each context, attachToFinding?: findingId}. Response: typed evidence keeping every raw flowId: {contexts:[{name,outcome,status,flowId,sameAsBaseline,sideEffect}], invalidBodyProbe, authOrder, authNotEnforced, hypotheses}"},
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
	{"GET", "/api/interception-setup", "Interception setup record: {setup:{version, proxyAddress, caFingerprint, hosts, enablers:[{tool, scriptHash, targetLibrary, method, hosts}]}, detected:{proxyAddress, caFingerprint, deviceProxy?}}. Optional ?serial= reads the adb device proxy. Version 0 = none recorded"},
	{"PUT", "/api/interception-setup", "Replace the interception setup record (proxy, CA fingerprint, pinning-bypass enablers, applicable hosts). Version increments only when content changes and prior versions are kept for flow provenance. Response: {setup}"},
	{"PUT", "/api/flows/{id}/interception", "Mark a bodiless CONNECT status-0 flow as pinning_blocked or not_intercepted so a capture gap is not read as a finding. Body: {annotation} — \"\" clears. Response: {flowId, annotation, tags}"},
	{"GET", "/api/engagement-brief", "Project engagement brief: {version, scope, authorisation, conductRules, rateLimits, doNotTouch, credentialPolicy}. Version 0 = none recorded"},
	{"PUT", "/api/engagement-brief", "Replace the engagement brief. Body: the six text fields (each max 16 KiB). Version increments only when content changes; reports cite it. Response: the saved brief"},
	{"GET", "/api/notes", "Project markdown notebook"},
	{"PUT", "/api/notes", "Replace project notebook. Body: {notes} — markdown; inline data-URL images are extracted into SQLite on save"},
	{"PATCH", "/api/notes", "Atomically append a block to the project notebook. Body: {appendText}"},
	{"POST", "/api/notes/images", "Upload an image for the notebook. Body: {mime, data} — data is raw base64 or a data: URL. Response: {id} for a markdown image ref"},
	{"GET", "/api/notes/images/{id}", "Serve a notebook image"},
	{"GET", "/api/activity", "AI/MCP activity feed"},
	{"POST", "/api/activity", "MCP-only activity transport; external HTTP requests are rejected."},
	{"DELETE", "/api/activity", "Clear activity feed"},
	{"GET", "/api/project", "Active project + switch targets. Each project includes category, createdAt, and openedAt (unix seconds). category is a display folder such as Clients/Acme and does not share data between projects."},
	{"POST", "/api/project/folder", "Set or clear a project's display folder. Body: {target} or {path}, plus {category}. category is a slash-separated label of at most 3 segments; empty clears it."},
	{"POST", "/api/project/switch", "Switch to another named project (re-exec). Body: {target} (plain project name) or {path} (absolute external folder) — mutually exclusive; target rejects path-like strings"},
	{"GET", "/api/oob/state", "OOB catcher state + interactions"},
	{"POST", "/api/oob/new", "Generate a new OOB callback token (no body). Response: {token, url}"},
	{"POST", "/api/oob/base", "Set public OOB base URL. Body: {baseUrl}"},
	{"DELETE", "/api/oob/interactions", "Clear OOB interaction log"},
	{"PUT", "/api/checks/disabled", "Disable/enable custom checks by id list. Body: {disabled: [ids]}"},
	{"GET", "/api/reference", "Machine-readable route catalog"},
	{"GET", "/api/mcp", "MCP tool descriptor + client config snippet"},
	{"GET", "/api/mcp/capabilities", "Live MCP contract metadata and supported finding fields"},
	{"GET", "/api/capabilities", "Alias of /api/mcp/capabilities: schemaVersion, schemaHash and supported finding fields (targetsSupported)"},
	{"POST", "/api/finding-cvss", "Evaluate a CVSS v4.0 vector without changing a finding. Body: {vector}; returns score, rating, severity (the required finding severity; NONE 0.0 maps to Info while rawRating keeps NONE), explanation and legacy (3.1) flag"},
	{"POST", "/api/redact", "Describe a secret without storing it. Body: {value}; returns {len, sha256_prefix, kind}. Paste the redacted form, never the value, into findings"},
	{"GET", "/api/flows/{id}/curl", "Reconstruct the flow's request as a runnable curl command"},
	{"GET", "/api/ui/{panel}", "Project-scoped UI state blob (panel=repeater|intruder|intruder-presets)"},
	{"PUT", "/api/ui/{panel}", "Save project-scoped UI state (JSON body)"},
	{"GET", "/api/packs/catalog", "List official bundled rule packs (+ installed flag)"},
	{"POST", "/api/packs/catalog/{name}/install", "Install an official bundled rule pack"},
	{"GET", "/api/export/har", "Export history as HAR (optional ?inScope=1)"},
	{"POST", "/api/import/har", "Import a HAR file as flows. Body: raw HAR 1.2 JSON document (not wrapped). Response: {imported: n}"},
	{"POST", "/api/import/postman", "Prepare a Postman Collection v2 JSON for Repeater. Body: raw collection or {collection, environment}; response: {name, requests, unresolved, warnings, skipped}. No History flows are created."},
	{"POST", "/api/import/burp", "Import Burp Suite Save-items XML as flows. Body: raw XML export (not native .burp). Response: {imported: n, skipped: n}"},
	{"GET", "/api/export/project", "Export a portable project (flows + rules + scope + settings)"},
	{"POST", "/api/import/project", "Import (merge) a project bundle. Body: {version, har, rules, scope, settings, notes?} — the JSON produced by GET /api/export/project. Response: {importedFlows, importedRules, importedScope}"},
	{"GET", "/api/export/full", "Download the active project as a lossless zip archive (DB + captured bodies)"},
	{"POST", "/api/import/full", "Upload a project zip and restore it as a new named project (?name=, ?overwrite=1)"},
	{"POST", "/api/export/full/file", "Write a full-project archive to a server-side path (for the local MCP agent)"},
	{"POST", "/api/import/full/file", "Restore a full-project archive from a server-side path into a new named project"},
	{"GET", "/api/merge/status", "Last peer sync presence (direction, peer URL, label, timestamp)"},
	{"POST", "/api/merge/file", "Push receiver: ingest an uploaded project archive and merge it into the active project"},
	{"POST", "/api/merge/pull", "Download a peer's project archive and merge ({peerUrl,key,label,dryRun?}) — dryRun returns add/skip preview without writing"},
	{"POST", "/api/merge/push", "Push archive to peer ({peerUrl,key,label,dryRun?}) — dryRun previews local inventory"},
	{"GET", "/api/vault/config", "Machine-wide vault client config ({url, hasKey}) — points at an interseptor vault"},
	{"PUT", "/api/vault/config", "Save vault client config. Body: {url?, key?}"},
	{"GET", "/api/vault/remote", "List projects on the configured vault (proxied)"},
	{"POST", "/api/vault/backup", "Snapshot the active project and upload to the vault. Body: {id?, label?}"},
	{"POST", "/api/vault/import", "Download a vault project into a new local project. Body: {id, name?, rev?, overwrite?}"},
	{"POST", "/api/vault/merge", "Download a vault project and merge into the active project. Body: {id, rev?, label?, dryRun?}"},
	{"GET", "/api/ca.crt", "Download the local CA certificate"},
	{"GET", "/openapi.json", "REST route discovery index (method/path/summary; not a full OpenAPI client-generation contract)"},
	{"GET", "/api/keys", "List API keys"},
	{"POST", "/api/keys", "Create an API key. Body: {label?, scope?, expiresIn?} — scope full (default)|read; expiresIn is seconds from now, 0/omitted = never. Response: {token, key} — the token is returned exactly once"},
	{"DELETE", "/api/keys/{id}", "Revoke an API key"},
	{"GET", "/api/allowlist", "List machine-global IP/CIDR allowlist entries + this request's clientIP (for Allow this IP)"},
	{"POST", "/api/allowlist", "Add an IP or CIDR that may access the UI/REST without an API key. Body: {cidr, label?}. /mcp still requires a key"},
	{"DELETE", "/api/allowlist/{id}", "Remove an allowlist entry"},
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
	{"GET", "/api/flows/retention", "Automatic retention policy (maxAgeHours, maxFlows; 0 = off)"},
	{"PUT", "/api/flows/retention", "Set the retention policy; body {maxAgeHours, maxFlows}"},
	{"POST", "/api/flows/retention/run", "Apply the retention policy now and return {deleted}"},
	{"GET", "/api/hosts/stats", "Per-host flow counts and byte totals, sorted desc by bytes. Response: {hosts:[{host,flows,bytes}],totalFlows,totalBytes}"},
	{"POST", "/api/human-input", "Register a human-input prompt and block up to 40s for the operator's answer ({message,options?})"},
	{"GET", "/api/human-input", "List pending human-input prompts raised by the AI"},
	{"GET", "/api/human-input/{id}", "Poll a human-input prompt for the operator's answer"},
	{"POST", "/api/human-input/{id}/respond", "Submit the operator's answer to a pending human-input prompt"},
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
		{"name": "finding_readiness", "desc": "Actionable finding and final report completeness checks"},
		{"name": "list_finding_revisions", "desc": "Immutable finding revision metadata"},
		{"name": "get_finding_revision", "desc": "Historical finding snapshot and field-level diff"},
		{"name": "restore_finding_revision", "desc": "Restore report content as a new revision"},
		{"name": "preview_finding_targets", "desc": "Preview target cleanup while preserving evidence"},
		{"name": "normalize_finding_targets", "desc": "Apply reviewer-approved path templates to a finding's targets (dry run by default)"},
		{"name": "evaluate_finding_cvss", "desc": "Preview CVSS v4 score and severity"},
		{"name": "redact_value", "desc": "Describe a secret as {len, sha256_prefix, kind}; the value is never stored"},
		{"name": "list_flows", "desc": "List/search captured proxy flows"},
		{"name": "get_flow", "desc": "Read a flow's raw request/response"},
		{"name": "analyze_flow", "desc": "Compact summary: headers, params, scanner hits, scope"},
		{"name": "audit_endpoint", "desc": "Consolidated triage: attack surfaces, auth context, passive findings, next steps"},
		{"name": "flow_as_curl", "desc": "Reconstruct a flow's request as a runnable curl command"},
		{"name": "diff_flows", "desc": "Diff two flows' responses: status, length, headers, body (baseline vs exploit)"},
		{"name": "set_note", "desc": "Annotate a flow with a note (\"\" clears it)"},
		{"name": "set_ws_frame_note", "desc": "Annotate one WebSocket frame of a flow"},
		{"name": "get_interception_setup", "desc": "Read how traffic is intercepted (proxy, CA fingerprint, pinning-bypass enablers) and its version"},
		{"name": "set_interception_setup", "desc": "Record the interception setup; version bumps only when content changes"},
		{"name": "annotate_flow_interception", "desc": "Mark a bodiless CONNECT status-0 flow pinning_blocked or not_intercepted"},
		{"name": "get_engagement_brief", "desc": "Read the project's authorisation/conduct brief (scope, rules, rate limits, do-not-touch, credential policy) and its version"},
		{"name": "set_engagement_brief", "desc": "Update the engagement brief; version bumps only when content changes"},
		{"name": "get_notes", "desc": "Read the project's shared markdown notebook"},
		{"name": "set_notes", "desc": "Replace the project's shared markdown notebook"},
		{"name": "append_notes", "desc": "Append a markdown block to the project notebook"},
		{"name": "tag_flow", "desc": "Attach tags to a flow for triage/grouping"},
		{"name": "untag_flow", "desc": "Remove tags from a flow (others kept)"},
		{"name": "list_tags", "desc": "List tags in use with flow counts"},
		{"name": "create_finding", "desc": "Create a canonical evidence-first finding with structured reproduction blocks"},
		{"name": "record_finding_from_flow", "desc": "Atomic creation of a finding with the captured flow attached as reproduction proof"},
		{"name": "list_findings", "desc": "List finding summaries and readiness; filter by severity/status/tag"},
		{"name": "get_finding", "desc": "Read one complete finding before editing its envelope or evidence"},
		{"name": "list_finding_tags", "desc": "List tags in use on findings with counts"},
		{"name": "update_finding", "desc": "Update canonical finding fields or structured blocks"},
		{"name": "add_finding_poc", "desc": "Attach a captured flow with role and proof statement"},
		{"name": "add_finding_image", "desc": "Attach a real screenshot with role, proof, and provenance"},
		{"name": "classify_finding_image", "desc": "Reviewer relabel of an attached image as a browser/device capture without re-upload"},
		{"name": "render_flow_preview", "desc": "Render a flow as a provenance-labeled HTTP PNG and optionally attach it"},
		{"name": "remove_finding_poc", "desc": "Detach a PoC flow from a finding"},
		{"name": "delete_finding", "desc": "Permanently delete a finding (cannot be undone)"},
		{"name": "export_report", "desc": "Export canonical findings as Markdown, self-contained HTML, or JSON"},
		{"name": "export_full_project", "desc": "Write a lossless portable archive of the whole project (DB + captured bodies) to a server-side .zip path"},
		{"name": "import_full_project", "desc": "Restore a full-project .zip archive into a new named project under ~/.interseptor/projects"},
		{"name": "vault_list", "desc": "List projects on the configured project vault"},
		{"name": "vault_backup", "desc": "Snapshot the active project and upload a revision to the vault"},
		{"name": "vault_import", "desc": "Download a vault project into a new local named project"},
		{"name": "vault_merge", "desc": "Download a vault project and merge into the active project (dryRun supported)"},
		{"name": "send_request", "desc": "Replay/mutate a request (Repeater)"},
		{"name": "start_intruder", "desc": "Run Sniper/Battering/Pitchfork/Cluster/Race payload attack"},
		{"name": "intruder_state", "desc": "Attack progress + results"},
		{"name": "run_scanner", "desc": "Passive scan of captured flows"},
		{"name": "list_issues", "desc": "Scanner findings"},
		{"name": "scan_report", "desc": "Findings as a Markdown report (grouped by severity)"},
		{"name": "list_checks", "desc": "List custom Starlark scanner checks"},
		{"name": "test_check", "desc": "Compile + run a check against a flow (no save)"},
		{"name": "save_check", "desc": "Create/update a validated custom scanner check"},
		{"name": "delete_check", "desc": "Delete a custom scanner check"},
		{"name": "list_codecs", "desc": "List project message codecs (encrypt/decrypt transforms)"},
		{"name": "test_codec", "desc": "Compile + match/decode a codec against a flow (no save)"},
		{"name": "save_codec", "desc": "Create/update a project message codec"},
		{"name": "delete_codec", "desc": "Delete a project message codec"},
		{"name": "get_flow_decoded", "desc": "App-layer decoded plaintext for a flow when a codec matches"},
		{"name": "encode_codec", "desc": "Re-encode edited plaintext to a wire body via a message codec"},
		{"name": "list_packs", "desc": "List installed rule packs"},
		{"name": "pack_info", "desc": "Show one installed rule pack's record"},
		{"name": "get_intercept", "desc": "Intercept state + hold queue"},
		{"name": "set_intercept", "desc": "Toggle request interception"},
		{"name": "set_response_intercept", "desc": "Toggle response interception"},
		{"name": "forward_request", "desc": "Forward a held request (optionally edited)"},
		{"name": "drop_request", "desc": "Drop a held request"},
		{"name": "forward_response", "desc": "Forward a held response (optionally edited)"},
		{"name": "drop_response", "desc": "Drop a held response"},
		{"name": "list_rules", "desc": "List match-&-replace rules"},
		{"name": "add_rule", "desc": "Add a request or response match-&-replace rule"},
		{"name": "update_rule", "desc": "Update a match-&-replace rule"},
		{"name": "delete_rule", "desc": "Delete a match-&-replace rule"},
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
		{"name": "set_authz", "desc": "Save authz identities (merge by name by default; mode:replace overwrites)"},
		{"name": "list_authz", "desc": "List authz identities with updatedAt/owner"},
		{"name": "add_authz_identity", "desc": "Add or update one authz identity by name"},
		{"name": "remove_authz_identity", "desc": "Remove one authz identity by name"},
		{"name": "authz_run", "desc": "Run authorization test (flowId or inScope:true)"},
		{"name": "authz_differential", "desc": "Anonymous vs low-priv vs admin differential on one flow, with typed finding evidence"},
		{"name": "auth_timeline", "desc": "Read-only login/session/CSRF/MFA timeline for a login flow (hypothesis-labelled, values hidden)"},
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

func (h *metaAPI) apiMCPCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcp.New(h.loopbackControlBase()).Capabilities())
}
