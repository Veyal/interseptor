package control

import "net/http"

// collRoute is one collection-family REST route. The same table drives mux
// registration and the apiRoutes catalog, so the two cannot drift
// (TestCollectionRoutesCatalogMatchesMux).
type collRoute struct {
	method, path, desc string
	handler            func(*collectionsAPI) http.HandlerFunc
}

var collRoutes = []collRoute{
	{"GET", "/api/collections", "List collections (no items). AI-source callers get secret-scrubbed data", func(c *collectionsAPI) http.HandlerFunc { return c.list }},
	{"POST", "/api/collections", "Create a collection. Body: {name, description?, auth?, events?, settings?, scopePolicy?}. Capabilities are never settable here; the AI channel cannot loosen scopePolicy", func(c *collectionsAPI) http.HandlerFunc { return c.create }},
	{"GET", "/api/collections/{uid}", "Collection with its flat item list: {collection, items}. AI-source callers get secret-scrubbed data", func(c *collectionsAPI) http.HandlerFunc { return c.get }},
	{"PUT", "/api/collections/{uid}", "Update a collection (optimistic: pass rev). Body as POST. Scripts a human newly writes in the UI are auto-trusted; imported or AI-written scripts stay quarantined", func(c *collectionsAPI) http.HandlerFunc { return c.update }},
	{"DELETE", "/api/collections/{uid}", "Delete a collection with its items, bound environments, trust and tokens (runs are kept)", func(c *collectionsAPI) http.HandlerFunc { return c.remove }},
	{"POST", "/api/collections/{uid}/items", "Create a folder or request item. Body: item fields (kind, name, parentUid?, method, url, headers, params, body, auth, events, ...)", func(c *collectionsAPI) http.HandlerFunc { return c.createItem }},
	{"GET", "/api/items/{uid}", "One item. AI-source callers get secret-scrubbed data", func(c *collectionsAPI) http.HandlerFunc { return c.getItem }},
	{"PUT", "/api/items/{uid}", "Update/move/reorder an item (parentUid, rank; pass rev for optimistic concurrency); previous state is kept as a revision", func(c *collectionsAPI) http.HandlerFunc { return c.updateItem }},
	{"DELETE", "/api/items/{uid}", "Delete an item (a folder deletes its descendants)", func(c *collectionsAPI) http.HandlerFunc { return c.removeItem }},
	{"POST", "/api/items/{uid}/duplicate", "Duplicate a request item next to the original", func(c *collectionsAPI) http.HandlerFunc { return c.duplicateItem }},
	{"GET", "/api/items/{uid}/examples", "List a request's saved examples", func(c *collectionsAPI) http.HandlerFunc { return c.listExamples }},
	{"POST", "/api/items/{uid}/examples", "Add an example (free-form object; id is assigned). Max 100 per request", func(c *collectionsAPI) http.HandlerFunc { return c.createExample }},
	{"PUT", "/api/items/{uid}/examples/{id}", "Replace one example", func(c *collectionsAPI) http.HandlerFunc { return c.updateExample }},
	{"DELETE", "/api/items/{uid}/examples/{id}", "Delete one example", func(c *collectionsAPI) http.HandlerFunc { return c.deleteExample }},

	{"GET", "/api/environments", "List environments and globals with declared variables. Secret current values are masked; ?reveal=1 works only for an interactive UI session", func(c *collectionsAPI) http.HandlerFunc { return c.listEnvs }},
	{"POST", "/api/environments", "Create an environment. Body: {name, kind: env|globals, collectionUid?, boundIdentity?, baseTargetPin?, variables?}", func(c *collectionsAPI) http.HandlerFunc { return c.createEnv }},
	{"GET", "/api/environments/{uid}", "One environment with variables", func(c *collectionsAPI) http.HandlerFunc { return c.getEnv }},
	{"PUT", "/api/environments/{uid}", "Update name, identity binding, base target pin and (optionally) replace declared variables", func(c *collectionsAPI) http.HandlerFunc { return c.updateEnv }},
	{"DELETE", "/api/environments/{uid}", "Delete an environment with its variables", func(c *collectionsAPI) http.HandlerFunc { return c.deleteEnv }},
	{"GET", "/api/variables/{kind}/{uid}", "Declared variables plus current values of one owner (kind: environment|collection|folder|request|global)", func(c *collectionsAPI) http.HandlerFunc { return c.getVars }},
	{"PUT", "/api/variables/{kind}/{uid}", "Replace the declared variables of one owner. Body: {variables:[{key,type default|secret|any,initialValue,enabled}]}. Secret initial values are blanked", func(c *collectionsAPI) http.HandlerFunc { return c.putVars }},
	{"PUT", "/api/variables/{kind}/{uid}/current", "Set one local current value (declares the variable when new). Body: {key, value, secret?}. A secret's value is never echoed", func(c *collectionsAPI) http.HandlerFunc { return c.putCurrent }},
	{"POST", "/api/variables/{kind}/{uid}/current/reset", "Drop all current values of an owner (reset to initial)", func(c *collectionsAPI) http.HandlerFunc { return c.resetCurrent }},
	{"POST", "/api/variables/resolve", "Resolve a template against the real variable layers. Body: {template, itemUid|collectionUid, envUid?}. Response: {value (secrets masked), uses, unresolved}", func(c *collectionsAPI) http.HandlerFunc { return c.resolvePreview }},

	{"POST", "/api/import/collection/preview", "Parse a collection file (Postman v2.0/2.1 collection/environment/globals, OpenAPI 3/Swagger 2 JSON or YAML, curl command, Insomnia v4/v5, Bruno .bru or {files:[{path,text}]} folder, HAR, Burp XML) and return the import report. Nothing is stored or executed. Body: raw file (max 64 MiB); ?format=auto|postman|openapi|curl|insomnia|bruno|bruno-files|har|burp", func(c *collectionsAPI) http.HandlerFunc { return c.importPreview }},
	{"POST", "/api/import/collection/commit", "Store a collection file (any format the preview accepts). Scripts arrive quarantined (no trust, no capabilities) until a human approves them through the UI. Body: raw file (max 64 MiB); ?format=auto|postman|openapi|curl|insomnia|bruno|bruno-files|har|burp", func(c *collectionsAPI) http.HandlerFunc { return c.importCommit }},

	{"POST", "/api/collections/send", "Send one collection item through the shared pipeline (variables, scripts, auth, scope guard, capture as a History flow). Body: {itemUid, envUid?, local?, noScripts?, persist: keep|discard, scopePolicy?}. Interactive default scope policy is warn; MCP is always the collection's policy (default block). Response: step result with tests, console, flow ids, unresolved vars", func(c *collectionsAPI) http.HandlerFunc { return c.send }},
	{"POST", "/api/collections/run", "Run a collection, folder or item list sequentially (max 1000 requests, 10 minutes). Body: {collectionUid, folderUid?, itemUids?, envUid?, persist?, bail?, delayMs?, noScripts?}. Scope policy defaults to block; untrusted scripts are skipped and counted", func(c *collectionsAPI) http.HandlerFunc { return c.run }},
	{"GET", "/api/collections/{uid}/runs", "Recent runs of a collection (newest first, max 50)", func(c *collectionsAPI) http.HandlerFunc { return c.listRuns }},
	{"GET", "/api/runs/{uid}", "Per-request results of one run", func(c *collectionsAPI) http.HandlerFunc { return c.getRun }},

	{"POST", "/api/runner/runs", "Start an asynchronous run owned by the server (not the request). Body as POST /api/collections/run plus {iterations?, rps?, failedFromRun?, data?: {name, text}}; persist defaults to ask for the UI (discard for the AI channel, which cannot ask). Returns 202 {runUid, status, plannedSteps}; follow it with GET /api/runner/runs/{uid}, its /events stream or the global {type:collrun} events", func(c *collectionsAPI) http.HandlerFunc { return c.startAsync }},
	{"GET", "/api/runner/runs", "Uids of runs that are still running", func(c *collectionsAPI) http.HandlerFunc { return c.activeRuns }},
	{"GET", "/api/runner/runs/{uid}", "Live progress of a run: status (running|paused|awaiting_persist|done|bailed|aborted|...), totals, results from offset ?since=N, pending variable writes while awaiting_persist, and the final report once finished", func(c *collectionsAPI) http.HandlerFunc { return c.runStatus }},
	{"GET", "/api/runner/runs/{uid}/events", "Server-Sent Events of one run: a snapshot, then start/item/paused/resumed/awaiting_persist/done events (results already masked)", func(c *collectionsAPI) http.HandlerFunc { return c.runEvents }},
	{"POST", "/api/runner/runs/{uid}/pause", "Hold the run before its next request", func(c *collectionsAPI) http.HandlerFunc { return c.runPause }},
	{"POST", "/api/runner/runs/{uid}/resume", "Continue a paused run", func(c *collectionsAPI) http.HandlerFunc { return c.runResume }},
	{"POST", "/api/runner/runs/{uid}/abort", "Abort the run, cancelling an in-flight request", func(c *collectionsAPI) http.HandlerFunc { return c.runAbort }},
	{"POST", "/api/runner/runs/{uid}/persist", "Answer a persist=ask prompt. UI-session only (AI/MCP/API-key callers get 403). Body: {keep: true|false}; 409 when the run is not waiting", func(c *collectionsAPI) http.HandlerFunc { return c.runPersist }},

	{"POST", "/api/collections/oauth/begin", "Start an OAuth 2.0 authorization-code (+PKCE) flow for a request's effective auth. UI-session only. Body: {itemUid, envUid?, local?}. Returns {authUrl, state, redirectUri}; open authUrl in a browser. The identity provider redirects to the callback on this port and the token is stored locally (never returned)", func(c *collectionsAPI) http.HandlerFunc { return c.oauthBegin }},
	{"GET", "/api/collections/oauth/callback", "OAuth 2.0 redirect target on the control port. Query: code, state (one-shot, 10 minute TTL). Exchanges the code for a token under the collection's scope policy and shows no token data", func(c *collectionsAPI) http.HandlerFunc { return c.oauthCallback }},
	{"GET", "/api/collections/{uid}/scripts", "Script review sheet: every distinct script with hash, trust state, analysis (APIs, modules, hosts, flags) and, for a UI session, source. AI-source callers get status only (no source)", func(c *collectionsAPI) http.HandlerFunc { return c.listScripts }},
	{"POST", "/api/collections/{uid}/trust", "Trust scripts and set the capability set. UI-session only: AI/MCP, API-key and agent callers get 403. Body: {confirm:true, all?|hashes?, capabilities?: [vars.read, vars.write, cookies.read, cookies.write, net.send, secrets.read]}", func(c *collectionsAPI) http.HandlerFunc { return c.approve }},
	{"POST", "/api/collections/{uid}/trust/revoke", "Revoke script trust (UI-session only). Body: {all?|hashes?}", func(c *collectionsAPI) http.HandlerFunc { return c.revoke }},
}

func init() {
	for _, r := range collRoutes {
		apiRoutes = append(apiRoutes, apiRoute{Method: r.method, Path: r.path, Desc: r.desc})
	}
	tools, _ := mcpDescriptor["tools"].([]map[string]string)
	mcpDescriptor["tools"] = append(tools, collectionMCPTools...)
}

// collectionMCPTools mirrors the tools internal/mcp registers in
// collections.go (TestMCPDescriptorMatchesRegistry keeps both in step).
var collectionMCPTools = []map[string]string{
	{"name": "list_collections", "desc": "List request collections and environments (secrets scrubbed)"},
	{"name": "get_collection", "desc": "One collection with its item tree and script approval status (secrets scrubbed)"},
	{"name": "run_request", "desc": "Send one collection item through the scoped pipeline; flow is captured in History"},
	{"name": "run_collection", "desc": "Run a collection/folder with scope policy block; untrusted scripts are skipped"},
	{"name": "set_variable", "desc": "Set a local current variable value (secret values are never echoed)"},
	{"name": "script_approval_status", "desc": "Read-only: which collection scripts are trusted (the AI can never trust scripts)"},
}

func (h *Hub) registerCollectionRoutes() {
	c := newCollectionsAPI(h)
	h.collAPI = c
	for _, r := range collRoutes {
		h.mux.HandleFunc(r.method+" "+r.path, r.handler(c))
	}
	c.registerMatrix()
}
