package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/store"
)

// ---- API keys ----

func (h *Hub) listKeys(w http.ResponseWriter, r *http.Request) {
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

func (h *Hub) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label string `json:"label"`
	}
	json.NewDecoder(r.Body).Decode(&in)
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

func (h *Hub) deleteKey(w http.ResponseWriter, r *http.Request) {
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
	{"GET", "/api/flows", "List captured proxy flows (filters: method, host, search, scheme, status, before, limit)"},
	{"GET", "/api/flows/{id}", "Flow detail (headers, body hashes, flags)"},
	{"GET", "/api/flows/{id}/raw", "Reconstructed raw request/response (?side=req|res)"},
	{"GET", "/api/flows/{id}/ws", "Captured WebSocket frames for a flow"},
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
	{"GET", "/api/views", "List saved history views"},
	{"POST", "/api/views", "Save the current filters as a named view"},
	{"DELETE", "/api/views/{id}", "Delete a saved view"},
	{"GET", "/api/scope", "List target-scope rules"},
	{"POST", "/api/scope", "Add a scope rule (include/exclude)"},
	{"PUT", "/api/scope/{id}", "Update a scope rule"},
	{"DELETE", "/api/scope/{id}", "Delete a scope rule"},
	{"GET", "/api/settings", "Get proxy/intercept settings"},
	{"PUT", "/api/settings", "Update settings (rebinds the proxy listener)"},
	{"GET", "/api/sysproxy", "System-proxy status (supported/enabled)"},
	{"POST", "/api/sysproxy", "Enable/disable the OS system proxy (macOS)"},
	{"GET", "/api/export/har", "Export history as HAR (optional ?inScope=1)"},
	{"POST", "/api/import/har", "Import a HAR file as flows"},
	{"GET", "/api/export/project", "Export a portable project (flows + rules + scope + settings)"},
	{"POST", "/api/import/project", "Import (merge) a project bundle"},
	{"GET", "/api/ca.crt", "Download the local CA certificate"},
	{"GET", "/api/keys", "List API keys"},
	{"POST", "/api/keys", "Create an API key"},
	{"DELETE", "/api/keys/{id}", "Revoke an API key"},
	{"GET", "/api/events", "Server-Sent Events stream of live updates"},
}

func (h *Hub) apiReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"baseUrl": "http://" + r.Host, "routes": apiRoutes})
}

// ---- MCP descriptor ----

var mcpDescriptor = map[string]any{
	"name":    "interceptor",
	"version": "0.1.0",
	"status":  "ready",
	"note":    "Run `interceptor` (this proxy/UI) first, then point your MCP client at `interceptor mcp` — a stdio MCP server that drives this engine over the control API. Set INTERCEPTOR_CONTROL_URL to override the default http://127.0.0.1:9966.",
	"transport": map[string]any{
		"type":    "stdio",
		"command": "interceptor",
		"args":    []string{"mcp"},
	},
	// Ready to paste into a Claude Desktop / Claude Code MCP config.
	"clientConfig": map[string]any{
		"mcpServers": map[string]any{
			"interceptor": map[string]any{
				"command": "interceptor",
				"args":    []string{"mcp"},
			},
		},
	},
	"tools": []map[string]string{
		{"name": "list_flows", "desc": "List/search captured proxy flows"},
		{"name": "get_flow", "desc": "Read a flow's raw request/response"},
		{"name": "send_request", "desc": "Replay/mutate a request (Repeater)"},
		{"name": "start_intruder", "desc": "Run a Sniper/Pitchfork payload attack"},
		{"name": "intruder_state", "desc": "Attack progress + results"},
		{"name": "run_scanner", "desc": "Passive scan of captured flows"},
		{"name": "list_issues", "desc": "Scanner findings"},
		{"name": "get_intercept", "desc": "Intercept state + hold queue"},
		{"name": "set_intercept", "desc": "Toggle request interception"},
		{"name": "set_response_intercept", "desc": "Toggle response interception"},
		{"name": "forward_request", "desc": "Forward a held request (optionally edited)"},
		{"name": "drop_request", "desc": "Drop a held request"},
		{"name": "list_rules", "desc": "List match-&-replace rules"},
		{"name": "add_rule", "desc": "Add a request match-&-replace rule"},
		{"name": "list_ws_frames", "desc": "WebSocket frames for a flow"},
		{"name": "list_scope", "desc": "List target-scope rules"},
		{"name": "add_scope_rule", "desc": "Add an in/out-of-scope rule"},
		{"name": "get_settings", "desc": "Proxy/intercept settings"},
		{"name": "ca_info", "desc": "How to trust the CA for HTTPS"},
	},
}

func (h *Hub) apiMCP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpDescriptor)
}
