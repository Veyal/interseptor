// Package mcp implements a Model Context Protocol server over stdio so an AI
// agent can operate Interceptor as a set of tools. It is a thin, well-described
// front end over the running control API (REST) — every tool maps to an endpoint
// the web UI also uses, so the human and the agent drive the same engine.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Veyal/interceptor/internal/httplines"
	"github.com/Veyal/interceptor/internal/version"
)

const protocolVersion = "2024-11-05"

// Server is an MCP stdio server backed by the control API at base.
type Server struct {
	base   string
	cl     *http.Client
	tools  map[string]tool
	order  []string
	report func(Activity) // called after each tool call; surfaces AI actions in the UI
}

type tool struct {
	description string
	schema      map[string]any
	call        func(args map[string]any) (string, error)
}

// Activity is a record of one MCP tool call. It is reported to the control plane
// after every call so a human watching the UI can see, live, what the AI is
// doing — which tool, the gist of the arguments, and the outcome.
type Activity struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"` // short, human-readable gist of the arguments
	OK      bool   `json:"ok"`
	Result  string `json:"result"` // first line of the result / error, truncated
	Ms      int64  `json:"ms"`
	Intent  string `json:"intent,omitempty"` // the AI's stated "why", if it passed one
}

// activitySummary renders the most informative arguments of a tool call into a
// short one-liner (e.g. "method=POST url=https://x/login"). Tool-agnostic: it
// picks known, high-signal keys in priority order so every tool reads sensibly.
func activitySummary(tool string, args map[string]any) string {
	order := []string{"method", "url", "target", "host", "path", "id", "op", "input", "match", "type", "side", "status", "message", "template", "source", "scheme", "enabled", "limit"}
	var parts []string
	for _, k := range order {
		v, ok := args[k]
		if !ok || v == nil {
			continue
		}
		sv := strings.TrimSpace(fmt.Sprint(v))
		if sv == "" {
			continue
		}
		if len(sv) > 60 {
			sv = sv[:60] + "…"
		}
		parts = append(parts, k+"="+sv)
		if len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// firstLine returns the first non-empty line of s, trimmed and capped at n runes.
func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if n > 0 && len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

// New builds an MCP server that talks to the control API at baseURL
// (e.g. http://127.0.0.1:9966).
func New(baseURL string) *Server {
	s := &Server{
		base:  strings.TrimRight(baseURL, "/"),
		cl:    &http.Client{Timeout: 60 * time.Second},
		tools: map[string]tool{},
	}
	s.report = s.postActivity
	s.registerTools()
	return s
}

// postActivity reports a tool call to the control plane (best-effort, async) so
// it shows up in the live AI-activity feed. Failures are ignored — observability
// must never affect the tool call itself.
func (s *Server) postActivity(a Activity) {
	b, err := json.Marshal(a)
	if err != nil {
		return
	}
	go func() {
		req, err := http.NewRequest(http.MethodPost, s.base+"/api/activity", bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
			resp.Body.Close()
		}
	}()
}

// Serve runs the JSON-RPC loop over newline-delimited messages until EOF.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	br := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			s.handleLine(t, out)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleLine(line []byte, out io.Writer) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		s.write(out, nil, nil, &rpcError{Code: -32700, Message: "parse error"})
		return
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	result, rerr := s.dispatch(req.Method, req.Params)
	if notification {
		return // notifications get no response
	}
	s.write(out, req.ID, result, rerr)
}

// mcpInstructions is the guidance surfaced to an AI agent on connect: the
// pentest workflow, plus a pointer to report bugs/gaps in Interceptor itself.
func mcpInstructions() string {
	return "Interceptor — an AI web-pentest workspace; a human watches everything you do and can take over manually, so record your work as you go.\n\n" +
		"SETUP: check_readiness (structured JSON blockers: OOB, scope, auth identities, login macro) → fix blockers → scope_from_url → ca_info + route traffic through proxy. Re-run check_readiness if list_flows/scans come back empty.\n\n" +
		"AUTH: list_flows tag=auth → promote_flow_to_authz (Surveyor, Admin, …) → authz_run inScope:true → set_login_macro_from_flow → run_login_macro (refresh CSRF).\n\n" +
		"RECON: start_discovery (wordlist optional — server default) → suggest_discovery_paths.\n\n" +
		"SCAN: run_scanner (passive) → active_scan arm:true inScope:true csrfAware:true → cross_host_token_replay mode:auto for SSO/JWT apps → oob_* for blind callbacks.\n\n" +
		"RECORD: create_finding (body blocks) → add_finding_poc position:N → update_finding impact/detail (detail-only updates preserve interleaved PoC blocks).\n\n" +
		"Everything you do is tagged AI. Pass optional `intent` on consequential tools. Use request_human_input before destructive or ambiguous steps.\n\n" +
		"IMPROVE INTERCEPTOR: this workspace is a tool under active development, separate from the target you are testing. If an Interceptor tool errors, returns something wrong, or is missing a capability you needed, report it (or ask the human to) at https://github.com/" + version.Repo + "/issues — include the tool name, what you expected, and what actually happened. Do not file issues about the target application there."
}

func (s *Server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(params, &p)
		ver := p.ProtocolVersion
		if ver == "" {
			ver = protocolVersion
		}
		return map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "interceptor", "version": version.Version},
			"instructions":    mcpInstructions(),
		}, nil
	case "tools/list":
		return map[string]any{"tools": s.toolList()}, nil
	case "tools/call":
		return s.callTool(params), nil
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		t := s.tools[name]
		out = append(out, map[string]any{
			"name":        name,
			"description": t.description,
			"inputSchema": t.schema,
		})
	}
	return out
}

func (s *Server) callTool(params json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolError("invalid params: " + err.Error())
	}
	text, err := s.runTool(p.Name, p.Arguments)
	if err != nil {
		return toolError(err.Error())
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

// runTool is the single execution path for every tool invocation, whether it
// arrives over JSON-RPC (callTool) or in-process (Call). It looks the tool up,
// runs it, and fires the best-effort Activity report — timing, summary, intent,
// and result snippet — identically for both callers, so the live AI-activity
// feed and FlagAI History tagging behave the same regardless of transport.
func (s *Server) runTool(name string, args map[string]any) (string, error) {
	t, ok := s.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	start := time.Now()
	text, err := t.call(args)
	if s.report != nil {
		a := Activity{Tool: name, Summary: activitySummary(name, args), OK: err == nil, Ms: time.Since(start).Milliseconds(), Intent: argStr(args, "intent")}
		if err != nil {
			a.Result = firstLine(err.Error(), 160)
		} else {
			a.Result = firstLine(text, 160)
		}
		s.report(a)
	}
	return text, err
}

// Call invokes a registered tool by name in-process, returning its text result.
// It is the seam the autonomous pentest engine (internal/autopwn) uses to drive
// all tools without a JSON-RPC round-trip. Activity logging + FlagAI History
// tagging still happen via the tool's own REST calls, exactly as for JSON-RPC:
// Call routes through the same runTool helper as callTool, so the Activity
// report (timing, summary, intent, outcome) is emitted identically.
func (s *Server) Call(name string, args map[string]any) (string, error) {
	return s.runTool(name, args)
}

func toolError(msg string) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

func (s *Server) write(out io.Writer, id json.RawMessage, result any, rerr *rpcError) {
	out.Write(append(s.marshalResponse(id, result, rerr), '\n'))
}

// marshalResponse builds a single JSON-RPC 2.0 response object.
func (s *Server) marshalResponse(id json.RawMessage, result any, rerr *rpcError) []byte {
	resp := map[string]any{"jsonrpc": "2.0"}
	if id != nil {
		resp["id"] = id
	} else {
		resp["id"] = nil
	}
	if rerr != nil {
		resp["error"] = rerr
	} else {
		resp["result"] = result
	}
	b, _ := json.Marshal(resp)
	return b
}

// ---- Streamable-HTTP transport ----

// ServeHTTP implements the MCP "Streamable HTTP" transport over a single
// endpoint. A client POSTs a JSON-RPC message (or batch) and receives the
// JSON-RPC response as application/json. The server is stateless — no
// Mcp-Session-Id is required — and offers no server-initiated SSE stream, so
// GET returns 405 (per spec). This lets a hosted/remote agent drive Interceptor
// without launching the `interceptor mcp` stdio subcommand. Bind localhost-only;
// it shares the (unauthenticated, local) trust model of the control API it fronts.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.servePost(w, r)
	case http.MethodOptions:
		w.Header().Set("Allow", "POST, GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		// This endpoint does not push server-initiated messages.
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "this MCP endpoint offers no SSE stream; POST a JSON-RPC message instead", http.StatusMethodNotAllowed)
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) servePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// A JSON-RPC batch (array) or a single message.
	if body[0] == '[' {
		var msgs []json.RawMessage
		if err := json.Unmarshal(body, &msgs); err != nil {
			s.writeJSON(w, s.marshalResponse(nil, nil, &rpcError{Code: -32700, Message: "parse error"}))
			return
		}
		responses := make([]json.RawMessage, 0, len(msgs))
		for _, m := range msgs {
			if resp := s.handleHTTPMessage(m); resp != nil {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted) // all notifications
			return
		}
		b, _ := json.Marshal(responses)
		s.writeJSON(w, b)
		return
	}

	resp := s.handleHTTPMessage(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // a notification — nothing to return
		return
	}
	s.writeJSON(w, resp)
}

// handleHTTPMessage dispatches one JSON-RPC message and returns the marshaled
// response, or nil if the message is a notification (no id).
func (s *Server) handleHTTPMessage(raw json.RawMessage) json.RawMessage {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.marshalResponse(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	result, rerr := s.dispatch(req.Method, req.Params)
	if notification {
		return nil
	}
	return s.marshalResponse(req.ID, result, rerr)
}

func (s *Server) writeJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// ---- REST plumbing ----

func (s *Server) apiGet(path string) (string, error) { return s.api(http.MethodGet, path, nil) }

// flowsExist reports whether a /api/flows query returned at least one flow.
func (s *Server) flowsExist(path string) bool {
	raw, err := s.apiGet(path)
	if err != nil {
		return false
	}
	var d struct {
		Flows []json.RawMessage `json:"flows"`
	}
	json.Unmarshal([]byte(raw), &d)
	return len(d.Flows) > 0
}

// inScopeTraffic uses the paginating /api/flows/inscope endpoint so readiness
// checks don't false-negative when recent history is mostly out-of-scope noise.
func (s *Server) inScopeTraffic() bool {
	raw, err := s.apiGet("/api/flows/inscope")
	if err != nil {
		return false
	}
	var d struct {
		InScope bool `json:"inScope"`
	}
	json.Unmarshal([]byte(raw), &d)
	return d.InScope
}

func (s *Server) api(method, path string, body any) (string, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, rdr)
	if err != nil {
		return "", err
	}
	// Marks every call as AI-originated so the control plane can tag the
	// resulting Repeater/Intruder/scan sends (FlagAI) and show them in History.
	req.Header.Set("X-Interceptor-Source", "ai")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("control API unreachable at %s — is `interceptor` running? (%v)", s.base, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s %s → %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

// ---- tool helpers ----

func argStr(a map[string]any, key string) string {
	v, ok := a[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func argInt(a map[string]any, key string, def int) int {
	switch x := a[key].(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return def
}

func argBool(a map[string]any, key string, def bool) bool {
	if b, ok := a[key].(bool); ok {
		return b
	}
	return def
}

// ---- argument validation (helpful errors) ----
//
// An AI driving these tools over MCP only ever sees the error string, so a bare
// "id is required" when it actually passed id:"abc" sends it into a retry loop.
// The req* helpers below report BOTH what was expected AND what was received
// (truncated, secrets masked) so the model can self-correct.

const argValueCap = 60 // max chars of an offending value echoed back

// looksSecret reports whether an argument key names a credential/token whose
// value should be masked rather than echoed into an error message.
func looksSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"token", "secret", "password", "passwd", "apikey", "api_key", "authorization", "cookie", "credential", "jwt", "bearer"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// describeValue renders a received argument value for an error message: typed,
// quoted if a string, truncated to argValueCap, and masked if the key names a
// secret. Used to show the AI exactly what it sent.
func describeValue(key string, v any) string {
	if v == nil {
		return "null"
	}
	if looksSecret(key) {
		return "(masked)"
	}
	switch x := v.(type) {
	case string:
		return strconv.Quote(truncRunes(x, argValueCap))
	default:
		return truncRunes(fmt.Sprint(v), argValueCap)
	}
}

// truncRunes caps s at n runes, appending an ellipsis when it had to cut.
func truncRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// reqStr returns a required, non-empty string argument or a helpful error. A
// present-but-empty or present-but-non-string value is reported with what was
// received so the AI can correct it.
func reqStr(a map[string]any, key string) (string, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return "", fmt.Errorf("%s is required (a non-empty string)", key)
	}
	s, isStr := v.(string)
	if !isStr {
		return "", fmt.Errorf("%s must be a string (got %T %s)", key, v, describeValue(key, v))
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s is required (got an empty string)", key)
	}
	return s, nil
}

// reqInt returns a required integer argument or a helpful error. It accepts JSON
// numbers, Go ints, and numeric strings (agents often quote numbers); anything
// else is reported with both the expected type and the offending value.
func reqInt(a map[string]any, key string) (int, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("%s is required (an integer)", key)
	}
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s must be an integer (got %T %s)", key, v, describeValue(key, v))
}

// reqFlowID returns the required flow id from "id" or "flowId" (agents guess
// both), or a helpful error naming whichever wrong value was supplied.
func reqFlowID(a map[string]any) (int, error) {
	_, hasID := a["id"]
	_, hasFlow := a["flowId"]
	if !hasID && !hasFlow {
		return 0, fmt.Errorf("id (or flowId) is required (an integer)")
	}
	key := "id"
	if !hasID {
		key = "flowId"
	}
	n, err := reqInt(a, key)
	if err != nil {
		// Re-key the message so it reads "id (or flowId) ...".
		return 0, fmt.Errorf("id (or flowId) must be an integer (got %T %s)", a[key], describeValue(key, a[key]))
	}
	if n == 0 {
		return 0, fmt.Errorf("id (or flowId) is required (a non-zero integer)")
	}
	return n, nil
}

// reqDiffID returns a required flow id read from the first of the supplied alias
// keys that is present (e.g. "a", "id1", "flowA"), or a helpful error naming the
// canonical key. Lets diff_flows accept the id-naming an agent guesses.
func reqDiffID(a map[string]any, keys ...string) (int, error) {
	for _, k := range keys {
		if _, ok := a[k]; ok {
			n, err := reqInt(a, k)
			if err != nil {
				return 0, fmt.Errorf("%s must be an integer (got %T %s)", keys[0], a[k], describeValue(k, a[k]))
			}
			if n == 0 {
				return 0, fmt.Errorf("%s is required (a non-zero flow id)", keys[0])
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s is required (an integer flow id)", keys[0])
}

// argHeaderLines normalizes MCP headers: "Key: Value" lines or a JSON object.
func argHeaderLines(a map[string]any, key string) (string, error) {
	v, ok := a[key]
	if !ok || v == nil {
		return "", nil
	}
	m, err := httplines.NormalizeArg(v)
	if err != nil {
		return "", err
	}
	return httplines.ToLines(m), nil
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n…[truncated %d more bytes — increase maxBytes]", len(s)-n)
}

// boundJSON keeps a tool result both VALID and small. Byte-truncating a JSON
// document mid-structure (the old approach) yields output an agent cannot parse
// — exactly when the result is large and interesting. Instead we cap the longest
// array (a bare top-level array, or the longest array field of a top-level
// object) at maxRows and record what was dropped, so the JSON stays parseable.
// Non-JSON (a scalar or an error string) falls back to a plain byte cap.
func boundJSON(raw string, maxRows int) string {
	trimmed := strings.TrimSpace(raw)
	// Bare top-level array → wrap as an object with capped items + counts.
	var arr []json.RawMessage
	if json.Unmarshal([]byte(trimmed), &arr) == nil {
		if len(arr) <= maxRows {
			return raw
		}
		kept, _ := json.Marshal(arr[:maxRows])
		return fmt.Sprintf(`{"items":%s,"_truncated":true,"_shown":%d,"_total":%d}`, kept, maxRows, len(arr))
	}
	// Top-level object → cap its single longest array field in place.
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &obj) == nil {
		key, n := "", 0
		for k, v := range obj {
			var a []json.RawMessage
			if json.Unmarshal(v, &a) == nil && len(a) > n {
				key, n = k, len(a)
			}
		}
		if key == "" || n <= maxRows {
			return raw
		}
		var a []json.RawMessage
		_ = json.Unmarshal(obj[key], &a)
		kept, _ := json.Marshal(a[:maxRows])
		obj[key] = kept
		obj["_truncated"] = json.RawMessage("true")
		obj["_truncatedField"] = json.RawMessage(strconv.Quote(key))
		obj["_shown"] = json.RawMessage(strconv.Itoa(maxRows))
		obj["_total"] = json.RawMessage(strconv.Itoa(n))
		if out, err := json.Marshal(obj); err == nil {
			return string(out)
		}
		return raw
	}
	// Not array/object JSON — bound by bytes so we still cap size.
	return truncate(raw, maxRows*64)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func p(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

// pt declares a parameter with no description — for self-evident names (id, url,
// method, body…) where a description would only cost tokens, not add meaning.
func pt(typ string) map[string]any { return map[string]any{"type": typ} }

// formatBytes renders a byte count as a human-readable string (B, KB, MB, GB).
// The UI's data-retention panel should use the same thresholds/units so numbers
// match: < 1 KB → "N B", < 1 MB → "N.N KB", < 1 GB → "N.N MB", else "N.N GB".
func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n < kb:
		return fmt.Sprintf("%d B", n)
	case n < mb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	case n < gb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	}
}

func (s *Server) add(name, desc string, schema map[string]any, call func(map[string]any) (string, error)) {
	s.tools[name] = tool{description: desc, schema: schema, call: call}
	s.order = append(s.order, name)
}

// ToolNames returns the registered tool names in registration order, so the UI
// descriptor / docs can be checked against the actual toolset (no silent drift).
func (s *Server) ToolNames() []string {
	return append([]string(nil), s.order...)
}

// registerTools wires every tool to a control-API endpoint.
func (s *Server) registerTools() {
	s.add("list_flows",
		"Search captured flows → compact rows (id, method, host, path, status). Filters optional.",
		obj(map[string]any{
			"host":   p("string", "substring"),
			"method": pt("string"),
			"search": p("string", "path substring"),
			"scheme": pt("string"),
			"status": p("integer", "class 1-5 (4=4xx)"),
			"tag":    p("string", "filter by tag (exact, case-insensitive)"),
			"tlsFailed": p("boolean", "only flows where TLS MITM failed (SSL pinning / untrusted CA)"),
			"limit":  p("integer", "default 50"),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			for _, k := range []string{"host", "method", "search", "scheme", "tag"} {
				if v := argStr(a, k); v != "" {
					q.Set(k, v)
				}
			}
			if v := argStr(a, "status"); v != "" {
				q.Set("status", v)
			}
			if argBool(a, "tlsFailed", false) {
				q.Set("tlsFailed", "1")
			}
			q.Set("limit", strconv.Itoa(argInt(a, "limit", 50)))
			return s.apiGet("/api/flows?" + q.Encode())
		})

	s.add("get_flow",
		"Read a flow's raw request and/or response (headers + body).",
		obj(map[string]any{
			"id":       pt("integer"),
			"side":     p("string", "req | res | both (default both)"),
			"maxBytes": pt("integer"),
		}, "id"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			max := argInt(a, "maxBytes", 4000)
			side := argStr(a, "side")
			if side == "" {
				side = "both"
			}
			get := func(sd string) string {
				raw, err := s.apiGet(fmt.Sprintf("/api/flows/%d/raw?side=%s", id, sd))
				if err != nil {
					return "(" + err.Error() + ")"
				}
				return truncate(raw, max)
			}
			if side == "both" {
				return "=== REQUEST ===\n" + get("req") + "\n\n=== RESPONSE ===\n" + get("res"), nil
			}
			return get(side), nil
		})

	s.add("analyze_flow",
		"Compact triage of a flow: URL/status, security headers, query params (injection points), passive findings, in-scope flag. Cheaper than get_flow for deciding what to inspect.",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/analyze", id))
		})

	s.add("flow_as_curl",
		"Render a flow's request as a runnable curl command.",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/curl", id))
		})

	s.add("diff_flows",
		"Diff two captured flows' responses — confirm whether a payload changed the response (baseline vs exploit). Returns status change (X→Y), response-length delta, headers added/removed/changed, and a bounded line-based body diff. Pass two flow ids as a/b (id1/id2 and flowA/flowB also accepted). Body comparison is capped at maxBytes (default 4000).",
		obj(map[string]any{
			"a":        p("integer", "baseline flow id"),
			"b":        p("integer", "comparison (e.g. exploit) flow id"),
			"maxBytes": p("integer", "cap on response-body bytes compared per side (default 4000)"),
		}, "a", "b"),
		func(args map[string]any) (string, error) {
			aID, err := reqDiffID(args, "a", "id1", "flowA")
			if err != nil {
				return "", err
			}
			bID, err := reqDiffID(args, "b", "id2", "flowB")
			if err != nil {
				return "", err
			}
			q := url.Values{}
			q.Set("a", strconv.Itoa(aID))
			q.Set("b", strconv.Itoa(bID))
			q.Set("format", "text")
			if mb := argInt(args, "maxBytes", 0); mb > 0 {
				q.Set("maxBytes", strconv.Itoa(mb))
			}
			return s.apiGet("/api/flows/diff?" + q.Encode())
		})

	s.add("set_note",
		"Annotate a flow with a note (record a finding for the operator; \"\" clears it). Visible in the UI inspector and on get_flow/list_flows.",
		obj(map[string]any{
			"id":   pt("integer"),
			"note": pt("string"),
		}, "id", "note"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			if _, err := s.api(http.MethodPut, fmt.Sprintf("/api/flows/%d/note", id), map[string]any{"note": argStr(a, "note")}); err != nil {
				return "", err
			}
			return fmt.Sprintf("noted flow %d", id), nil
		})

	s.add("tag_flow",
		"Attach short tags to a flow for triage/grouping (e.g. \"auth idor candidate\"). Tags are added to any existing ones (not replaced), shown as chips in History, and the human can filter by them. Comma- or space-separated; lowercased slugs.",
		obj(map[string]any{
			"id":     pt("integer"),
			"tags":   p("string", "comma- or space-separated tags"),
			"intent": p("string", "optional: a short why, shown to the human"),
		}, "id", "tags"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			tags := strings.FieldsFunc(argStr(a, "tags"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
			if len(tags) == 0 {
				return "", fmt.Errorf("tags is required (got %s) — pass at least one comma- or space-separated tag", describeValue("tags", a["tags"]))
			}
			if _, err := s.api(http.MethodPost, "/api/flows/tags", map[string]any{"flowIds": []int{id}, "add": tags}); err != nil {
				return "", err
			}
			return fmt.Sprintf("tagged flow %d with %s", id, strings.Join(tags, ", ")), nil
		})

	s.add("untag_flow",
		"Remove tags from a flow without replacing the rest. Comma- or space-separated tag slugs; same bulk API as tag_flow but with remove.",
		obj(map[string]any{
			"id":   pt("integer"),
			"tags": p("string", "comma- or space-separated tags to remove"),
		}, "id", "tags"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			tags := strings.FieldsFunc(argStr(a, "tags"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
			if len(tags) == 0 {
				return "", fmt.Errorf("tags is required (got %s) — pass at least one comma- or space-separated tag", describeValue("tags", a["tags"]))
			}
			if _, err := s.api(http.MethodPost, "/api/flows/tags", map[string]any{"flowIds": []int{id}, "remove": tags}); err != nil {
				return "", err
			}
			return fmt.Sprintf("removed %s from flow %d", strings.Join(tags, ", "), id), nil
		})

	s.add("list_tags",
		"List the tags in use across the project's flows, with how many flows carry each — so you can reuse existing tags instead of inventing near-duplicates.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/tags") })

	s.add("get_notes",
		"Read the project's shared markdown notebook — the operator's scratchpad for credentials, scope, findings and to-dos. Read it before editing with set_notes.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			raw, err := s.apiGet("/api/notes")
			if err != nil {
				return "", err
			}
			var d struct {
				Notes string `json:"notes"`
			}
			json.Unmarshal([]byte(raw), &d)
			if d.Notes == "" {
				return "(notebook is empty)", nil
			}
			return d.Notes, nil
		})

	s.add("set_notes",
		"Replace the project's shared markdown notebook. Call get_notes first and edit the returned text so existing content isn't clobbered; use append_notes to only add.",
		obj(map[string]any{"notes": pt("string")}, "notes"),
		func(a map[string]any) (string, error) {
			if _, err := s.api(http.MethodPut, "/api/notes", map[string]any{"notes": argStr(a, "notes")}); err != nil {
				return "", err
			}
			return "notes saved", nil
		})

	s.add("append_notes",
		"Append a markdown block to the project notebook (e.g. a new finding) without rewriting the rest.",
		obj(map[string]any{"text": pt("string")}, "text"),
		func(a map[string]any) (string, error) {
			raw, err := s.apiGet("/api/notes")
			if err != nil {
				return "", err
			}
			var d struct {
				Notes string `json:"notes"`
			}
			json.Unmarshal([]byte(raw), &d)
			joined := argStr(a, "text")
			if d.Notes != "" {
				joined = d.Notes + "\n\n" + joined
			}
			if _, err := s.api(http.MethodPut, "/api/notes", map[string]any{"notes": joined}); err != nil {
				return "", err
			}
			return "appended to notes", nil
		})

	// ---- findings: structured, curated vulnerability records (the AI's durable
	// memory; the human reviews/curates them in the Findings tab) ----
	s.add("create_finding",
		"Record a confirmed/suspected vulnerability as a structured finding (the AI's durable memory the human reviews). Always write a description and define the security IMPACT (what an attacker gains / business consequence) first, then attach PoCs with add_finding_poc. Returns the new finding with its id and a clickable UI URL. severity=Critical|High|Medium|Low|Info; status defaults to open.",
		obj(map[string]any{
			"title":    pt("string"),
			"severity": pt("string"),
			"status":   pt("string"),
			"target":   pt("string"),
			"detail":   pt("string"),
			"evidence": pt("string"),
			"impact":   p("string", "the security impact — what an attacker gains / business consequence"),
			"cvss":     p("string", "CVSS score or vector string, e.g. 7.5 or CVSS:3.1/AV:N/..."),
			"body":     p("string", "JSON array string of blocks [{type:'text',md},{type:'flow',flowId,note}] for full interleaved control"),
			"intent":   p("string", "optional: a short 'why' shown to the human in the Activity feed"),
		}, "title"),
		func(a map[string]any) (string, error) {
			if _, err := reqStr(a, "title"); err != nil {
				return "", err
			}
			reqBody := map[string]any{
				"title": argStr(a, "title"), "severity": argStr(a, "severity"), "status": argStr(a, "status"),
				"target": argStr(a, "target"), "detail": argStr(a, "detail"),
				"evidence": argStr(a, "evidence"), "source": "ai",
			}
			// impact is the primary field; fix is accepted for back-compat but not advertised.
			if v := argStr(a, "impact"); v != "" {
				reqBody["impact"] = v
			} else if v := argStr(a, "fix"); v != "" {
				reqBody["fix"] = v
			}
			if v := argStr(a, "cvss"); v != "" {
				reqBody["cvss"] = v
			}
			if v := argStr(a, "body"); v != "" {
				reqBody["body"] = v
			}
			result, err := s.api(http.MethodPost, "/api/findings", reqBody)
			if err != nil {
				return result, err
			}
			// Append the UI deep-link URL so the human can navigate directly.
			var f struct {
				ID int64 `json:"id"`
			}
			if jsonErr := json.Unmarshal([]byte(result), &f); jsonErr == nil && f.ID > 0 {
				result += fmt.Sprintf("\n\nUI: %s/#finding-%d", s.base, f.ID)
			}
			return result, nil
		})

	s.add("list_findings",
		"List the project's findings (with their attached PoC flows), optionally filtered by severity or status (open|verified|false_positive|wont_fix|fixed). Use this to track progress and avoid re-reporting.",
		obj(map[string]any{"severity": pt("string"), "status": pt("string")}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			if v := argStr(a, "severity"); v != "" {
				q.Set("severity", v)
			}
			if v := argStr(a, "status"); v != "" {
				q.Set("status", v)
			}
			p := "/api/findings"
			if len(q) > 0 {
				p += "?" + q.Encode()
			}
			return s.apiGet(p)
		})

	s.add("update_finding",
		"Update a finding's status or any field (e.g. mark verified once you've confirmed the PoC, or false_positive, or set the security impact). Only the fields you pass are changed. Returns the updated finding with a clickable UI URL.",
		obj(map[string]any{
			"id":       pt("integer"),
			"status":   pt("string"),
			"severity": pt("string"),
			"title":    pt("string"),
			"target":   pt("string"),
			"detail":   pt("string"),
			"evidence": pt("string"),
			"impact":   p("string", "the security impact — what an attacker gains / business consequence"),
			"cvss":     p("string", "CVSS score or vector string, e.g. 7.5 or CVSS:3.1/AV:N/..."),
			"body":     p("string", "JSON array string of blocks [{type:'text',md},{type:'flow',flowId,note}] for full interleaved control"),
		}, "id"),
		func(a map[string]any) (string, error) {
			id, err := reqInt(a, "id")
			if err != nil {
				return "", err
			}
			if id == 0 {
				return "", fmt.Errorf("id is required (a non-zero finding id)")
			}
			body := map[string]any{}
			for _, k := range []string{"status", "severity", "title", "target", "detail", "evidence", "impact", "cvss", "body"} {
				if v, ok := a[k]; ok {
					body[k] = v
				}
			}
			// fix accepted for back-compat but not advertised in schema.
			if v, ok := a["fix"]; ok {
				body["fix"] = v
			}
			result, err := s.api(http.MethodPatch, fmt.Sprintf("/api/findings/%d", id), body)
			if err != nil {
				return result, err
			}
			// Append the UI deep-link URL.
			result += fmt.Sprintf("\n\nUI: %s/#finding-%d", s.base, id)
			return result, nil
		})

	s.add("add_finding_poc",
		"Attach a captured flow (a request/response from list_flows) to a finding as proof-of-concept evidence. Attach the baseline and the exploit requests so the human can reproduce it. Optional note explains what the flow demonstrates. Optional position (0-based block index) inserts the flow block at that index in the body; omit to append at end.",
		obj(map[string]any{
			"findingId": pt("integer"),
			"flowId":    pt("integer"),
			"note":      pt("string"),
			"position":  p("integer", "0-based block index to insert the flow at; omit to append at end"),
		}, "findingId", "flowId"),
		func(a map[string]any) (string, error) {
			fid, err := reqInt(a, "findingId")
			if err != nil {
				return "", err
			}
			flow, err := reqInt(a, "flowId")
			if err != nil {
				return "", err
			}
			if fid == 0 || flow == 0 {
				return "", fmt.Errorf("findingId and flowId are required (non-zero integers; got findingId=%d flowId=%d)", fid, flow)
			}
			reqBody := map[string]any{"flowId": flow, "note": argStr(a, "note")}
			if pos, ok := a["position"]; ok && pos != nil {
				reqBody["position"] = pos
			}
			return s.api(http.MethodPost, fmt.Sprintf("/api/findings/%d/flows", fid), reqBody)
		})

	s.add("remove_finding_poc",
		"Detach a PoC flow from a finding.",
		obj(map[string]any{"findingId": pt("integer"), "flowId": pt("integer")}, "findingId", "flowId"),
		func(a map[string]any) (string, error) {
			fid, err := reqInt(a, "findingId")
			if err != nil {
				return "", err
			}
			flow, err := reqInt(a, "flowId")
			if err != nil {
				return "", err
			}
			if fid == 0 || flow == 0 {
				return "", fmt.Errorf("findingId and flowId are required (non-zero integers; got findingId=%d flowId=%d)", fid, flow)
			}
			return s.api(http.MethodDelete, fmt.Sprintf("/api/findings/%d/flows/%d", fid, flow), nil)
		})

	s.add("export_report",
		"Render the engagement report: curated findings with PoC flows. Passive scan is omitted by default — pass includeIssues=true for the appendix. format=html for HTML.",
		obj(map[string]any{
			"includeIssues": p("boolean", "include passive-scan issues appendix (default false)"),
			"format":        p("string", "md (default) or html"),
		}),
		func(a map[string]any) (string, error) {
			p := "/api/findings/report"
			var q []string
			if argBool(a, "includeIssues", false) {
				q = append(q, "issues=1")
			}
			if strings.ToLower(argStr(a, "format")) == "html" {
				q = append(q, "format=html")
			}
			if len(q) > 0 {
				p += "?" + strings.Join(q, "&")
			}
			return s.apiGet(p)
		})

	s.add("export_full_project",
		"Write a lossless, portable archive of the ENTIRE active project (a consistent DB snapshot + every captured body blob) to a zip file on the server filesystem. Unlike export_report/HAR, restoring it reproduces the project byte-for-byte on another machine. The global CA and custom checks are not included. Returns the path and size.",
		obj(map[string]any{
			"path": p("string", "absolute path to write the .zip to, e.g. /backups/acme.zip"),
		}, "path"),
		func(a map[string]any) (string, error) {
			path := strings.TrimSpace(argStr(a, "path"))
			if path == "" {
				return "", fmt.Errorf("path is required (absolute .zip path to write)")
			}
			return s.api(http.MethodPost, "/api/export/full/file", map[string]any{"path": path})
		})

	s.add("import_full_project",
		"Restore a full-project archive (from export_full_project) on the server filesystem into a NEW named project under ~/.interceptor/projects/<name>. Refuses to overwrite an existing project unless overwrite=true. After import, switch to the project to open it.",
		obj(map[string]any{
			"path":      p("string", "absolute path to the .zip archive to restore"),
			"name":      p("string", "new project name (plain name, no path separators)"),
			"overwrite": p("boolean", "replace an existing project of that name (default false)"),
		}, "path", "name"),
		func(a map[string]any) (string, error) {
			path := strings.TrimSpace(argStr(a, "path"))
			name := strings.TrimSpace(argStr(a, "name"))
			if path == "" || name == "" {
				return "", fmt.Errorf("path and name are required")
			}
			return s.api(http.MethodPost, "/api/import/full/file", map[string]any{
				"path": path, "name": name, "overwrite": argBool(a, "overwrite", false),
			})
		})

	s.add("send_request",
		"Send an HTTP request (Repeater) and record it. Returns the flow id+status; get_flow that id for the body.",
		obj(map[string]any{
			"method":  pt("string"),
			"url":     p("string", "absolute URL"),
			"headers": map[string]any{"oneOf": []any{map[string]any{"type": "string", "description": "'Key: Value' lines"}, map[string]any{"type": "object", "description": "header map e.g. {\"User-Agent\":\"bot\"}"}}},
			"body":    pt("string"),
		}, "url"),
		func(a map[string]any) (string, error) {
			hdr, err := argHeaderLines(a, "headers")
			if err != nil {
				return "", err
			}
			out, err := s.api(http.MethodPost, "/api/repeater/send", map[string]any{
				"method": argStr(a, "method"), "url": argStr(a, "url"),
				"headers": hdr, "body": argStr(a, "body"),
			})
			if err != nil {
				return "", err
			}
			return out + "\n(call get_flow with this id for the full response)", nil
		})

	s.add("start_intruder",
		"Fuzz a request. Mark fuzz points with §…§ in template. attackType: sniper=one position at a time; battering=same payload in every § at once; pitchfork=parallel lists; cluster=cartesian product (one list per §). payloads=list of lists.",
		obj(map[string]any{
			"target":     p("string", "scheme://host[:port]"),
			"template":   p("string", "raw request with §…§"),
			"attackType": p("string", "sniper | battering | pitchfork | cluster"),
			"payloads":   map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		}, "target", "template"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intruder/start", map[string]any{
				"target": argStr(a, "target"), "template": argStr(a, "template"),
				"attackType": argStr(a, "attackType"), "payloads": a["payloads"],
			})
		})

	s.add("intruder_state",
		"Intruder progress + results (status/length/time per payload; anomalies flagged).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet("/api/intruder/state")
			return boundJSON(out, 200), err
		})

	s.add("run_scanner",
		"Passive scan over captured flows → findings (severity/title/target/evidence/fix). No requests sent.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/scanner/run", nil) })

	s.add("list_issues", "List current scanner findings.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/scanner/issues") })

	s.add("scan_report",
		"Passive findings as a Markdown report, grouped by severity.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/scanner/report") })

	s.add("list_checks",
		"List custom Starlark checks (id, source, compile error). They run on every scan.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/checks") })

	s.add("test_check",
		"Compile+run a Starlark check against a flow WITHOUT saving (returns findings or the error). Iterate, then save_check. Omit flowId for the latest flow. Shape: def check(flow): return [finding(severity,title,detail=,evidence=,fix=)]. flow has method/scheme/host/port/path/status/mime, req_body/res_body, req_header(n)/res_header(n), query_param(n); builtin re_search(pat,text).",
		obj(map[string]any{
			"source": p("string", "Starlark source"),
			"flowId": p("integer", "default latest"),
		}, "source"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/checks/test", map[string]any{"source": argStr(a, "source"), "flowId": argInt(a, "flowId", 0)})
		})

	s.add("save_check",
		"Save a Starlark check by id (letters/digits/-/_); must compile. Runs on every scan. test_check first.",
		obj(map[string]any{
			"id":     pt("string"),
			"source": p("string", "Starlark source"),
		}, "id", "source"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPut, "/api/checks/"+url.PathEscape(argStr(a, "id")), map[string]any{"source": argStr(a, "source")})
		})

	s.add("delete_check", "Delete a custom check by id.",
		obj(map[string]any{"id": pt("string")}, "id"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodDelete, "/api/checks/"+url.PathEscape(argStr(a, "id")), nil)
		})

	s.add("get_intercept", "Intercept state + current hold queue.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/intercept") })

	s.add("set_intercept", "Enable/disable request interception (hold requests to edit/drop).",
		obj(map[string]any{"enabled": pt("boolean")}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intercept/toggle", map[string]any{"enabled": argBool(a, "enabled", false)})
		})

	s.add("set_response_intercept", "Enable/disable response interception (hold responses to edit/drop).",
		obj(map[string]any{"enabled": pt("boolean")}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intercept/response/toggle", map[string]any{"enabled": argBool(a, "enabled", false)})
		})

	s.add("forward_request", "Forward a held request (optionally with edited raw bytes).",
		obj(map[string]any{"id": pt("integer"), "raw": p("string", "edited raw request (optional)")}, "id"),
		func(a map[string]any) (string, error) {
			body := map[string]any{}
			if r := argStr(a, "raw"); r != "" {
				body["raw"] = r
			}
			return s.api(http.MethodPost, fmt.Sprintf("/api/intercept/%d/forward", argInt(a, "id", 0)), body)
		})

	s.add("drop_request", "Drop a held request.", obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, fmt.Sprintf("/api/intercept/%d/drop", argInt(a, "id", 0)), nil)
		})

	s.add("list_rules", "List match-&-replace rules.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/rules") })

	s.add("add_rule", "Add a request-side match-&-replace rule (regex).",
		obj(map[string]any{
			"type":    p("string", "req-header | req-body"),
			"match":   p("string", "regex"),
			"replace": pt("string"),
			"enabled": p("boolean", "default true"),
		}, "type", "match"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/rules", map[string]any{
				"type": argStr(a, "type"), "match": argStr(a, "match"),
				"replace": argStr(a, "replace"), "enabled": argBool(a, "enabled", true),
			})
		})

	s.add("list_ws_frames", "List a flow's WebSocket frames (dir/opcode/length/preview).",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet(fmt.Sprintf("/api/flows/%d/ws", argInt(a, "id", 0)))
			return boundJSON(out, 200), err
		})

	s.add("ws_send",
		"Open a fresh WebSocket, send one message, return the server's reply frames.",
		obj(map[string]any{
			"url":     p("string", "ws:// or wss://"),
			"message": pt("string"),
			"binary":  p("boolean", "send a binary frame"),
			"headers": p("string", "extra handshake 'Key: Value' lines"),
		}, "url", "message"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/ws/send", map[string]any{
				"url": argStr(a, "url"), "message": argStr(a, "message"),
				"binary": argBool(a, "binary", false), "headers": argStr(a, "headers"),
			})
		})

	s.add("list_scope", "List target-scope rules (which hosts/paths are in scope).", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/scope") })

	s.add("add_scope_rule",
		"Add a scope rule. host allows a leading wildcard (*.acme.com); path is a prefix. Scope focuses history/intercept/scanners.",
		obj(map[string]any{
			"action":  p("string", "include | exclude"),
			"host":    p("string", "e.g. *.acme.com"),
			"path":    p("string", "prefix (optional)"),
			"scheme":  p("string", "http | https (optional)"),
			"enabled": p("boolean", "default true"),
		}, "action"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/scope", map[string]any{
				"action": argStr(a, "action"), "host": argStr(a, "host"), "path": argStr(a, "path"),
				"scheme": argStr(a, "scheme"), "enabled": argBool(a, "enabled", true),
			})
		})

	s.add("get_settings", "Proxy/intercept settings (bind address, intercept on/off).", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/settings") })

	s.add("set_session",
		"Auth headers auto-applied to every Repeater/Intruder send (e.g. Authorization/Cookie) so sends stay authenticated. enabled=false to stop. Use hostHeaders when testing multiple targets simultaneously — each host gets its own auth, overriding the global headers for that hostname.",
		obj(map[string]any{
			"enabled":     pt("boolean"),
			"headers":     p("string", "'Key: Value' lines — global fallback applied to all in-scope hosts"),
			"hostHeaders": p("object", "per-host auth overrides: {\"hostname\": \"Key: Value\\nKey2: Value2\"} — replaces global headers for matching hosts only"),
		}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/session", map[string]any{
				"enabled":     argBool(a, "enabled", false),
				"headers":     argStr(a, "headers"),
				"hostHeaders": a["hostHeaders"],
			})
		})

	s.add("active_scan",
		"ACTIVE scan — sends real attack payloads (reflected XSS, SQLi, SSTI, open redirect, path traversal, timing OS-cmd-injection) to an in-scope target. Authorized targets only. arm=true confirms authorization (session gate, required once). Target one flowId, or inScope=true for all in-scope endpoints. Async — poll active_scan_state.",
		obj(map[string]any{
			"arm":         p("boolean", "confirm authorization + enable"),
			"flowId":      p("integer", "scan one flow's endpoint"),
			"inScope":     p("boolean", "scan all in-scope endpoints"),
			"maxRequests": p("integer", "probe budget (default 2000)"),
			"csrfAware":   p("boolean", "Laravel CSRF bootstrap + skip endpoints after 419 storms (default true)"),
		}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/activescan/start", map[string]any{
				"arm": argBool(a, "arm", false), "flowId": argInt(a, "flowId", 0),
				"inScope": argBool(a, "inScope", false), "maxRequests": argInt(a, "maxRequests", 0),
				"csrfAware": argBool(a, "csrfAware", true),
			})
		})

	s.add("active_scan_state", "Active-scan progress + confirmed findings.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet("/api/activescan")
			return boundJSON(out, 200), err
		})

	s.add("active_scan_stop", "Stop the running active scan (kill switch).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/activescan/stop", nil) })

	s.add("autopwn_start",
		"Launch a FULLY AUTONOMOUS pentest run: the engine reads captured in-scope history, plans and executes active testing using Interceptor's own tools, verifies every candidate through a 4-gate verifier, and files ONLY machine-proven findings (no 'possible' issues). Scope-gated — refuses to start without target-scope rules; own listeners are never probed. Budgets (maxRequests/maxTokens/maxWallMs) are hard kill switches. Returns immediately with a runId; the run continues in the background — poll autopwn_state. targetHint optionally steers the planning phase.",
		obj(map[string]any{
			"maxRequests": p("integer", "hard cap on real HTTP sends (0 = engine default)"),
			"maxTokens":   p("integer", "advisory LLM token ceiling (0 = unbounded)"),
			"maxWallMs":   p("integer", "hard wall-clock kill in milliseconds (0 = unbounded)"),
			"targetHint":  p("string", "optional operator steer for the planning phase"),
		}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/autopwn/start", map[string]any{
				"budget": map[string]any{
					"maxRequests": argInt(a, "maxRequests", 0),
					"maxTokens":   argInt(a, "maxTokens", 0),
					"maxWallMs":   argInt(a, "maxWallMs", 0),
				},
				"targetHint": argStr(a, "targetHint"),
			})
		})

	s.add("autopwn_state", "Autonomous-pentest run progress: phase, budget consumption, and candidate/verified/filed/rejected counts for the current or last run.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/autopwn/state") })

	s.add("autopwn_stop", "Stop the running autonomous pentest (kill switch — cancels the run context immediately).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/autopwn/stop", nil) })

	s.add("decode",
		"Encode/decode a string. op: base64encode/base64decode, urlencode/urldecode, hexencode/hexdecode, htmlencode/htmldecode, jwtdecode, smart (auto-detect one layer).",
		obj(map[string]any{
			"op":    p("string", "see list above"),
			"input": pt("string"),
		}, "op", "input"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/decode", map[string]any{"op": argStr(a, "op"), "input": argStr(a, "input")})
		})

	s.add("ca_info", "How to trust the CA so HTTPS can be intercepted (proxy address + CA location).", obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			settings, _ := s.apiGet("/api/settings")
			return fmt.Sprintf("To intercept HTTPS, point the client at the proxy and trust the local CA (a one-time MANUAL step per client — Interceptor never edits the OS trust store for you).\nSettings: %s\nCA download: %s/api/ca.crt (also at ~/.interceptor/ca/ca.crt).\nTrust it on the client:\n• macOS: open the .crt → Keychain Access → System keychain → set the Interceptor CA to Always Trust.\n• Windows: double-click → Install Certificate → Current User → Trusted Root Certification Authorities.\n• Linux (Debian/Ubuntu): copy to /usr/local/share/ca-certificates/interceptor.crt → sudo update-ca-certificates. (Fedora/RHEL: /etc/pki/ca-trust/source/anchors/ → sudo update-ca-trust.)\n• Firefox: Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import.\n• Android (adb): android_setup (USB reverse + CA) or Settings → TLS → Android (ADB).\n• iOS: ios_setup (Simulator + profile) or Settings → TLS → iOS → download .mobileconfig → install in Safari → Certificate Trust Settings.\n• curl/tools one-off: curl --cacert ~/.interceptor/ca/ca.crt -x http://127.0.0.1:8080 https://… (or SSL_CERT_FILE / REQUESTS_CA_BUNDLE).\nHTTP needs none of this — the CA is only for decrypting HTTPS.", strings.TrimSpace(settings), s.base), nil
		})

	s.add("android_status",
		"List USB-connected Android devices (requires adb on PATH): serial, model, emulator hint, suggested CA mode, LAN host, device proxy state.",
		obj(map[string]any{
			"serial": p("string", "optional device serial for proxy status"),
		}),
		func(a map[string]any) (string, error) {
			q := ""
			if ser := argStr(a, "serial"); ser != "" {
				q = "?serial=" + url.QueryEscape(ser)
			}
			return s.apiGet("/api/android/status" + q)
		})

	s.add("android_setup",
		"One-click Android HTTPS intercept via adb: set global proxy + install CA. USB mode uses adb reverse (default). Wi‑Fi mode uses host LAN IP (Interceptor must bind 0.0.0.0). caMode auto picks system CA for emulators, user CA for physical devices.",
		obj(map[string]any{
			"serial":    p("string", "device serial when multiple are connected"),
			"proxyMode": p("string", "usb (default) or wifi"),
			"caMode":    p("string", "user, system, or auto (default auto)"),
			"wifiHost":  p("string", "LAN IP override for wifi mode (default: device proxy auto-selection)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{}
			if v := argStr(a, "serial"); v != "" {
				body["serial"] = v
			}
			if v := argStr(a, "proxyMode"); v != "" {
				body["proxyMode"] = v
			}
			if v := argStr(a, "caMode"); v != "" {
				body["caMode"] = v
			}
			if v := argStr(a, "wifiHost"); v != "" {
				body["wifiHost"] = v
			}
			return s.api(http.MethodPost, "/api/android/setup", body)
		})

	s.add("android_teardown",
		"Clear Android global proxy and adb reverse. Optionally remove the Interceptor system CA (rooted device/emulator).",
		obj(map[string]any{
			"serial":         p("string", "device serial when multiple connected"),
			"removeSystemCA": p("boolean", "remove Interceptor CA from /system/etc/security/cacerts/ (default false)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{"removeSystemCA": argBool(a, "removeSystemCA", false)}
			if v := argStr(a, "serial"); v != "" {
				body["serial"] = v
			}
			return s.api(http.MethodPost, "/api/android/unproxy", body)
		})

	s.add("ios_status",
		"List iOS simulators (Xcode simctl) and USB iPhones (libimobiledevice): UDID, name, boot state, LAN host, profile URL path.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			return s.apiGet("/api/ios/status")
		})

	s.add("ios_setup",
		"One-click iOS intercept setup. Simulator (macOS+Xcode): install CA via simctl + open configuration profile (proxy + CA) in Safari. Physical iPhone: returns profileUrl — open on device Safari (Wi‑Fi mode; same network as Interceptor). Does not bypass SSL pinning.",
		obj(map[string]any{
			"udid":      p("string", "simulator UDID or omit for auto-select booted simulator / sole device"),
			"proxyMode": p("string", "localhost (simulator, default) or wifi (physical device)"),
			"wifiHost":  p("string", "LAN IP for Wi‑Fi proxy (default: device proxy auto-selection)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{"proxyMode": argStr(a, "proxyMode")}
			if v := argStr(a, "udid"); v != "" {
				body["udid"] = v
			}
			if v := argStr(a, "wifiHost"); v != "" {
				body["wifiHost"] = v
			}
			return s.api(http.MethodPost, "/api/ios/setup", body)
		})

	s.add("ios_install_ca",
		"Install Interceptor CA into a booted iOS Simulator via simctl (macOS + Xcode only). Physical iPhones must use ios_setup profile instead.",
		obj(map[string]any{
			"udid": p("string", "simulator UDID or omit for booted simulator"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{}
			if v := argStr(a, "udid"); v != "" {
				body["udid"] = v
			}
			return s.api(http.MethodPost, "/api/ios/install-ca", body)
		})

	s.add("ios_ssh_status",
		"Check jailbroken iOS device SSH reachability and authentication. Requires OpenSSH on device (default root@IP:22). Credentials are not persisted.",
		obj(map[string]any{
			"host":     p("string", "device IP or hostname (required for auth check)"),
			"port":     p("integer", "SSH port (default 22)"),
			"user":     p("string", "SSH user (default root)"),
			"password": p("string", "SSH password (e.g. default jailbreak passwords)"),
			"keyPath":  p("string", "path to SSH private key on the Interceptor host"),
		}),
		func(a map[string]any) (string, error) {
			host := argStr(a, "host")
			if host == "" {
				return s.apiGet("/api/ios/ssh/status")
			}
			body := map[string]any{"host": host}
			if v := argInt(a, "port", 0); v > 0 {
				body["port"] = v
			}
			if v := argStr(a, "user"); v != "" {
				body["user"] = v
			}
			if v := argStr(a, "password"); v != "" {
				body["password"] = v
			}
			if v := argStr(a, "keyPath"); v != "" {
				body["keyPath"] = v
			}
			return s.api(http.MethodPost, "/api/ios/ssh/status", body)
		})

	s.add("ios_ssh_setup",
		"One-click jailbroken iOS HTTPS intercept via SSH: opens mobileconfig on device (CA + global HTTP proxy). Operator must tap Install and enable Certificate Trust Settings. Does not bypass SSL pinning.",
		obj(map[string]any{
			"host":      p("string", "device IP or hostname (required)"),
			"port":      p("integer", "SSH port (default 22)"),
			"user":      p("string", "SSH user (default root)"),
			"password":  p("string", "SSH password"),
			"keyPath":   p("string", "path to SSH private key on the Interceptor host"),
			"proxyHost": p("string", "LAN IP embedded in profile proxy (default: device proxy auto-selection)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{"host": argStr(a, "host")}
			if v := argInt(a, "port", 0); v > 0 {
				body["port"] = v
			}
			if v := argStr(a, "user"); v != "" {
				body["user"] = v
			}
			if v := argStr(a, "password"); v != "" {
				body["password"] = v
			}
			if v := argStr(a, "keyPath"); v != "" {
				body["keyPath"] = v
			}
			if v := argStr(a, "proxyHost"); v != "" {
				body["proxyHost"] = v
			}
			return s.api(http.MethodPost, "/api/ios/ssh/setup", body)
		})

	s.add("ios_ssh_install_ca",
		"Open the Interceptor mobileconfig (CA + proxy) on a jailbroken iOS device via SSH. Same manual trust steps as ios_ssh_setup but only triggers profile install UI.",
		obj(map[string]any{
			"host":      p("string", "device IP or hostname (required)"),
			"port":      p("integer", "SSH port (default 22)"),
			"user":      p("string", "SSH user (default root)"),
			"password":  p("string", "SSH password"),
			"keyPath":   p("string", "path to SSH private key on the Interceptor host"),
			"proxyHost": p("string", "LAN IP for profile proxy settings (default: device proxy auto-selection)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{"host": argStr(a, "host")}
			if v := argInt(a, "port", 0); v > 0 {
				body["port"] = v
			}
			if v := argStr(a, "user"); v != "" {
				body["user"] = v
			}
			if v := argStr(a, "password"); v != "" {
				body["password"] = v
			}
			if v := argStr(a, "keyPath"); v != "" {
				body["keyPath"] = v
			}
			if v := argStr(a, "proxyHost"); v != "" {
				body["proxyHost"] = v
			}
			return s.api(http.MethodPost, "/api/ios/ssh/install-ca", body)
		})

	s.add("check_readiness",
		"Pre-flight setup checklist (structured JSON): proxy, scope, traffic, tls_intercept (pinning/CA detection), OOB, auth identities, login macro, active-scan arm state. Returns ready + blockers with fix hints. Run at session start or when list_flows/scans are empty.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			raw, err := s.apiGet("/api/readiness")
			if err != nil {
				return "", err
			}
			settings, _ := s.apiGet("/api/settings")
			return raw + "\n\nCA (HTTPS): " + s.base + "/api/ca.crt\nSettings: " + strings.TrimSpace(settings), nil
		})

	s.add("detect_ssl_pinning",
		"Diagnose why mobile/HTTPS traffic isn't appearing: distinguishes SSL pinning or untrusted CA (CONNECT reached proxy but TLS handshake failed) from no traffic or cleartext-only. Returns verdict (ok | tls_blocked | no_traffic | no_https), counts, recent failures, and fix hints. Call after using the app when History is empty or incomplete.",
		obj(map[string]any{
			"host": p("string", "optional host substring to focus diagnosis"),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			if v := argStr(a, "host"); v != "" {
				q.Set("host", v)
			}
			raw, err := s.apiGet("/api/tls-diagnosis?" + q.Encode())
			if err != nil {
				return "", err
			}
			var rep struct {
				Verdict string `json:"verdict"`
				Detail  string `json:"detail"`
				Fix     string `json:"fix"`
			}
			_ = json.Unmarshal([]byte(raw), &rep)
			return rep.Verdict + " — " + rep.Detail + "\n\nNote: Interceptor detects pinning but cannot bypass it (canBypass=false). Bypass on the device: Frida/objection, patched APK, or emulator + system CA.\n\n" + raw, nil
		})

	s.add("scope_from_url",
		"Focus scope on a target by URL — adds an include scope rule for the URL's host (and scheme). Call this first when you start on a target (e.g. \"pentest https://app.acme.com\"). wildcard=true scopes *.<host> (subdomains) instead of the exact host.",
		obj(map[string]any{
			"url":      p("string", "target URL or host, e.g. https://app.acme.com/login"),
			"wildcard": p("boolean", "scope *.<host> (subdomains) instead of the exact host (default false)"),
		}, "url"),
		func(a map[string]any) (string, error) {
			rawURL, err := reqStr(a, "url")
			if err != nil {
				return "", err
			}
			raw := strings.TrimSpace(rawURL)
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" {
				if u, err = url.Parse("https://" + raw); err != nil || u.Hostname() == "" {
					return "", fmt.Errorf("invalid url: %q", raw)
				}
			}
			host := u.Hostname()
			if argBool(a, "wildcard", false) {
				host = "*." + host
			}
			scheme := u.Scheme
			if scheme != "http" && scheme != "https" {
				scheme = ""
			}
			return s.api(http.MethodPost, "/api/scope", map[string]any{
				"action": "include", "host": host, "scheme": scheme, "enabled": true,
			})
		})

	s.add("request_human_input",
		"Pause and ASK THE HUMAN before a high-impact or ambiguous action (e.g. \"found IDOR on /api/user/{id} — fuzz ids 1-100?\", or to confirm scope). The question appears in the operator's UI. Returns their answer if they reply within ~40s; otherwise returns a pending id — call get_human_response(id) to retrieve it. Use this instead of guessing or exceeding the human's authority.",
		obj(map[string]any{
			"message": p("string", "the question, or what you want to do and why"),
			"options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional suggested answers, e.g. [\"yes\",\"no\"]"},
		}, "message"),
		func(a map[string]any) (string, error) {
			if _, err := reqStr(a, "message"); err != nil {
				return "", err
			}
			body := map[string]any{"message": argStr(a, "message")}
			if opts, ok := a["options"].([]any); ok && len(opts) > 0 {
				body["options"] = opts
			}
			raw, err := s.api(http.MethodPost, "/api/human-input", body)
			if err != nil {
				return "", err
			}
			var pr struct {
				ID       int64  `json:"id"`
				Answered bool   `json:"answered"`
				Answer   string `json:"answer"`
			}
			json.Unmarshal([]byte(raw), &pr)
			if pr.Answered {
				return "Human answered: " + pr.Answer, nil
			}
			return fmt.Sprintf("No answer yet (the prompt is showing in the operator's UI). Do other read-only work if useful, then call get_human_response with id=%d to fetch their reply before proceeding.", pr.ID), nil
		})

	s.add("get_human_response",
		"Retrieve the human's answer to an earlier request_human_input (poll this until they've answered).",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			id, err := reqInt(a, "id")
			if err != nil {
				return "", err
			}
			if id == 0 {
				return "", fmt.Errorf("id is required (a non-zero pending-prompt id)")
			}
			raw, err := s.apiGet(fmt.Sprintf("/api/human-input/%d", id))
			if err != nil {
				return "", err
			}
			var pr struct {
				Answered bool   `json:"answered"`
				Answer   string `json:"answer"`
			}
			json.Unmarshal([]byte(raw), &pr)
			if pr.Answered {
				return "Human answered: " + pr.Answer, nil
			}
			return "Still pending — the human hasn't answered yet. Poll again shortly.", nil
		})

	s.add("host_stats",
		"Show a table of captured hosts sorted by byte volume (flows + bytes per host, plus totals). Call this before prune_history to decide which hosts to keep or delete.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			raw, err := s.apiGet("/api/hosts/stats")
			if err != nil {
				return "", err
			}
			var d struct {
				Hosts []struct {
					Host  string `json:"host"`
					Flows int64  `json:"flows"`
					Bytes int64  `json:"bytes"`
				} `json:"hosts"`
				TotalFlows int64 `json:"totalFlows"`
				TotalBytes int64 `json:"totalBytes"`
			}
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				return raw, nil // fall back to raw JSON if parse fails
			}
			if len(d.Hosts) == 0 {
				return "No flows captured yet.", nil
			}
			var sb strings.Builder
			sb.WriteString("HOST                                           FLOWS    SIZE\n")
			sb.WriteString("--------------------------------------------------------------\n")
			for _, h := range d.Hosts {
				fmt.Fprintf(&sb, "%-46s %5d    %s\n", h.Host, h.Flows, formatBytes(h.Bytes))
			}
			sb.WriteString("--------------------------------------------------------------\n")
			fmt.Fprintf(&sb, "%-46s %5d    %s\n", "TOTAL", d.TotalFlows, formatBytes(d.TotalBytes))
			return sb.String(), nil
		})

	s.add("prune_history",
		"DESTRUCTIVE: delete flows by host pattern to keep the project small. hosts is a comma- or newline-separated list of host patterns (wildcards like *.acme.com are supported). mode=delete removes flows matching the listed hosts; mode=keepOnly removes everything EXCEPT the listed hosts. keepOnly with no hosts is rejected (prevents accidental wipe). Changes are broadcast live to open History views.",
		obj(map[string]any{
			"hosts": p("string", "comma- or newline-separated host patterns, e.g. 'noise.com,*.cdn.com'"),
			"mode":  p("string", "delete (default) | keepOnly"),
		}, "hosts"),
		func(a map[string]any) (string, error) {
			hostsRaw := argStr(a, "hosts")
			mode := argStr(a, "mode")
			if mode == "" {
				mode = "delete"
			}
			// Split on commas and newlines, trim whitespace, drop empties.
			var hosts []string
			for _, part := range strings.FieldsFunc(hostsRaw, func(r rune) bool {
				return r == ',' || r == '\n' || r == '\r'
			}) {
				if h := strings.TrimSpace(part); h != "" {
					hosts = append(hosts, h)
				}
			}
			raw, err := s.api(http.MethodPost, "/api/flows/purge", map[string]any{
				"hosts": hosts,
				"mode":  mode,
			})
			if err != nil {
				return "", err
			}
			var d struct {
				Deleted      int64 `json:"deleted"`
				RemovedFiles int64 `json:"removedFiles"`
				FreedBytes   int64 `json:"freedBytes"`
			}
			if jsonErr := json.Unmarshal([]byte(raw), &d); jsonErr != nil {
				return raw, nil
			}
			return fmt.Sprintf("deleted %d flows · freed %s (mode=%s)", d.Deleted, formatBytes(d.FreedBytes), mode), nil
		})

	s.add("start_discovery",
		"Content discovery (forced-browse): brute-force paths from a wordlist against a base URL. Scope-gated. Async — poll discovery_state.",
		obj(map[string]any{
			"baseUrl":    p("string", "absolute base URL e.g. https://target/"),
			"wordlist":   p("string", "newline-separated paths (optional — server default if empty)"),
			"extensions": p("string", "e.g. .php .bak"),
			"threads":    p("integer", "1–64, default 20"),
			"delayMs":    p("integer", "ms between dispatches"),
			"recursive":  p("boolean", "recurse into found directories"),
			"maxDepth":   p("integer", "recursion depth"),
			"record":     p("boolean", "save hits to History/Map (default true)"),
		}, "baseUrl"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/discovery/start", map[string]any{
				"baseUrl": argStr(a, "baseUrl"), "wordlist": argStr(a, "wordlist"),
				"extensions": argStr(a, "extensions"), "threads": argInt(a, "threads", 0),
				"delayMs": argInt(a, "delayMs", 0), "recursive": argBool(a, "recursive", false),
				"maxDepth": argInt(a, "maxDepth", 0), "record": argBool(a, "record", true),
			})
		})

	s.add("discovery_state", "Discovery run progress + found paths.", obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet("/api/discovery/state")
			return boundJSON(out, 400), err
		})

	s.add("stop_discovery", "Stop the running content-discovery scan.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/discovery/stop", nil) })

	s.add("suggest_discovery_paths",
		"Suggest paths to brute-force on a host: merges captured-history seeds with optional AI guesses (needs AI key for the latter).",
		obj(map[string]any{
			"host":    p("string", "target hostname"),
			"baseUrl": p("string", "alternative to host — derive hostname from this URL"),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			if h := argStr(a, "host"); h != "" {
				q.Set("host", h)
			}
			if b := argStr(a, "baseUrl"); b != "" {
				q.Set("baseUrl", b)
			}
			return s.apiGet("/api/discovery/suggest?" + q.Encode())
		})

	s.add("run_login_macro",
		"Run the recorded login macro now — refreshes session Cookie/Authorization headers from the login response. Configure via Settings → Session or set_session with loginMacro.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/session/login/run", nil) })

	s.add("get_authz", "List saved authorization-test identities (name + auth headers per role).", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/authz") })

	s.add("set_authz", "Save authorization-test identities.",
		obj(map[string]any{
			"identities": p("array", "objects with name + headers (Cookie/Authorization lines)"),
		}, "identities"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/authz", map[string]any{"identities": a["identities"]})
		})

	s.add("authz_run",
		"Replay captured endpoint(s) under each identity and diff responses — IDOR / broken access control.",
		obj(map[string]any{
			"flowId":     p("integer", "single flow to test"),
			"inScope":    p("boolean", "bulk: deduped in-scope endpoints"),
			"maxFlows":   p("integer", "bulk cap (default 30, max 100)"),
			"skipStatic": p("boolean", "bulk: skip .js/.css/images (default true)"),
		}),
		func(a map[string]any) (string, error) {
			body := map[string]any{}
			if v := argInt(a, "flowId", 0); v > 0 {
				body["flowId"] = v
			}
			if argBool(a, "inScope", false) {
				body["inScope"] = true
			}
			if v := argInt(a, "maxFlows", 0); v > 0 {
				body["maxFlows"] = v
			}
			if _, ok := a["skipStatic"]; ok {
				body["skipStatic"] = argBool(a, "skipStatic", true)
			}
			raw, err := s.api(http.MethodPost, "/api/authz/run", body)
			return boundJSON(raw, 600), err
		})

	s.add("authz_check_sessions",
		"Replay one flow (e.g. GET /api/me) under each identity — 401/403 with auth headers marks session invalid.",
		obj(map[string]any{"flowId": pt("integer")}, "flowId"),
		func(a map[string]any) (string, error) {
			id, err := reqInt(a, "flowId")
			if err != nil {
				return "", err
			}
			if id == 0 {
				return "", fmt.Errorf("flowId is required (a non-zero integer)")
			}
			return s.api(http.MethodPost, "/api/authz/check-sessions", map[string]any{"flowId": id})
		})

	s.add("cross_host_token_replay",
		"Take a JWT from one flow and replay the same path to every unique in-scope host in history — automates cross-environment token confusion detection (e.g. qa-internal Bearer accepted on qa-external because they share a JWT secret).",
		obj(map[string]any{
			"flowId":    pt("integer"),
			"jwtFlowId": p("integer", "flow whose JWT to extract — defaults to flowId"),
			"jwt":       p("string", "raw JWT string (alternative to jwtFlowId)"),
			"mode":      p("string", "auto | bearer | path — auto picks path replay for SSO URL/path tokens"),
		}, "flowId"),
		func(a map[string]any) (string, error) {
			body := map[string]any{"flowId": argInt(a, "flowId", 0)}
			if v := argInt(a, "jwtFlowId", 0); v > 0 {
				body["jwtFlowId"] = v
			}
			if v := argStr(a, "jwt"); v != "" {
				body["jwt"] = v
			}
			if v := argStr(a, "mode"); v != "" {
				body["mode"] = v
			}
			return s.api(http.MethodPost, "/api/authz/cross-host-replay", body)
		})

	s.add("oob_state", "Out-of-band callback catcher: enabled flag, base URL, recent interactions.", obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet("/api/oob/state")
			return boundJSON(out, 400), err
		})

	s.add("oob_new", "Generate a new blind-callback token/URL (requires OOB enabled + reachable base URL).", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/oob/new", nil) })

	s.add("oob_set_base", "Set the public OOB base URL the target can reach (e.g. https://xyz.ngrok.io/oob).",
		obj(map[string]any{"baseUrl": pt("string")}, "baseUrl"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/oob/base", map[string]any{"baseUrl": argStr(a, "baseUrl")})
		})

	s.add("oob_enable", "Enable the OOB interaction catcher (one-click; required before oob_new).",
		obj(map[string]any{"enabled": p("boolean", "default true")}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPut, "/api/settings", map[string]any{"oobEnabled": argBool(a, "enabled", true)})
		})

	s.add("get_flow_auth",
		"Extract Cookie/Authorization/XSRF headers from a captured flow for authz or session setup.",
		obj(map[string]any{"flowId": pt("integer")}, "flowId"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			return s.apiGet(fmt.Sprintf("/api/authz/flow-auth/%d", id))
		})

	s.add("promote_flow_to_authz",
		"Promote a flow's auth headers into an authz identity (role) for authz_run diffing.",
		obj(map[string]any{
			"flowId": pt("integer"),
			"name":   p("string", "identity label e.g. Surveyor, Admin"),
			"merge":  p("boolean", "update existing same name or append (default true)"),
		}, "flowId", "name"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			body := map[string]any{"name": argStr(a, "name"), "merge": argBool(a, "merge", true)}
			return s.api(http.MethodPost, fmt.Sprintf("/api/authz/from-flow/%d", id), body)
		})

	s.add("set_login_macro_from_flow",
		"Capture a flow's request as the login macro (CSRF/session refresh before active scan).",
		obj(map[string]any{
			"flowId":      pt("integer"),
			"enabled":     p("boolean", "default true"),
			"refreshSecs": p("integer", "auto-refresh interval"),
			"reauthOn401": p("boolean", "default true"),
		}, "flowId"),
		func(a map[string]any) (string, error) {
			id, err := reqFlowID(a)
			if err != nil {
				return "", err
			}
			body := map[string]any{}
			if _, ok := a["enabled"]; ok {
				body["enabled"] = argBool(a, "enabled", true)
			}
			if v := argInt(a, "refreshSecs", 0); v > 0 {
				body["refreshSecs"] = v
			}
			if _, ok := a["reauthOn401"]; ok {
				body["reauthOn401"] = argBool(a, "reauthOn401", true)
			}
			return s.api(http.MethodPost, fmt.Sprintf("/api/session/login/from-flow/%d", id), body)
		})

	s.add("set_login_macro",
		"Configure the login macro directly (raw HTTP request + target URL).",
		obj(map[string]any{
			"enabled":     pt("boolean"),
			"target":      p("string", "scheme://host[:port]"),
			"request":     p("string", "raw HTTP request (request line + headers + body)"),
			"refreshSecs": p("integer", "auto-refresh interval; 0 = manual/401 only"),
			"reauthOn401": p("boolean", "retry sends after re-login on 401"),
		}, "enabled", "target", "request"),
		func(a map[string]any) (string, error) {
			lm := map[string]any{
				"enabled":     argBool(a, "enabled", false),
				"target":      argStr(a, "target"),
				"request":     argStr(a, "request"),
				"refreshSecs": argInt(a, "refreshSecs", 0),
				"reauthOn401": argBool(a, "reauthOn401", true),
			}
			return s.api(http.MethodPost, "/api/session", map[string]any{"loginMacro": lm})
		})

	s.add("test_login_macro",
		"Dry-run the login macro — returns login response status and headers it would capture (does not apply session).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/session/login/test", nil)
		})

	s.add("get_discovery_wordlist",
		"Return the built-in default content-discovery wordlist (same fallback start_discovery uses when wordlist is empty).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			return s.apiGet("/api/discovery/wordlist")
		})
}
