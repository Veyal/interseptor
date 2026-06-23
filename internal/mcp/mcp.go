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
)

const protocolVersion = "2024-11-05"

// Server is an MCP stdio server backed by the control API at base.
type Server struct {
	base  string
	cl    *http.Client
	tools map[string]tool
	order []string
}

type tool struct {
	description string
	schema      map[string]any
	call        func(args map[string]any) (string, error)
}

// New builds an MCP server that talks to the control API at baseURL
// (e.g. http://127.0.0.1:9966).
func New(baseURL string) *Server {
	s := &Server{
		base:  strings.TrimRight(baseURL, "/"),
		cl:    &http.Client{Timeout: 60 * time.Second},
		tools: map[string]tool{},
	}
	s.registerTools()
	return s
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
			"serverInfo":      map[string]any{"name": "interceptor", "version": "0.2.1"},
			"instructions":    "Interceptor: an intercepting HTTP/HTTPS proxy. Use these tools to list and read captured flows, replay/mutate requests (send_request), fuzz (start_intruder), passively scan (run_scanner), and control interception. Bodies are bounded by default; pass maxBytes to read more.",
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
	t, ok := s.tools[p.Name]
	if !ok {
		return toolError("unknown tool: " + p.Name)
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	text, err := t.call(p.Arguments)
	if err != nil {
		return toolError(err.Error())
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
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

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n…[truncated %d more bytes — increase maxBytes]", len(s)-n)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func p(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func (s *Server) add(name, desc string, schema map[string]any, call func(map[string]any) (string, error)) {
	s.tools[name] = tool{description: desc, schema: schema, call: call}
	s.order = append(s.order, name)
}

// registerTools wires every tool to a control-API endpoint.
func (s *Server) registerTools() {
	s.add("list_flows",
		"List/search captured proxy flows (compact summaries). Filter by host, method, path search, scheme, status class (1-5 → 1xx-5xx).",
		obj(map[string]any{
			"host":   p("string", "host substring"),
			"method": p("string", "exact HTTP method"),
			"search": p("string", "path substring"),
			"scheme": p("string", "http or https"),
			"status": p("integer", "status class 1-5 (e.g. 4 = 4xx)"),
			"limit":  p("integer", "max rows (default 50)"),
		}),
		func(a map[string]any) (string, error) {
			q := url.Values{}
			for _, k := range []string{"host", "method", "search", "scheme"} {
				if v := argStr(a, k); v != "" {
					q.Set(k, v)
				}
			}
			if v := argStr(a, "status"); v != "" {
				q.Set("status", v)
			}
			q.Set("limit", strconv.Itoa(argInt(a, "limit", 50)))
			return s.apiGet("/api/flows?" + q.Encode())
		})

	s.add("get_flow",
		"Read a captured flow's raw request and/or response (headers + body), bounded by maxBytes.",
		obj(map[string]any{
			"id":       p("integer", "flow id"),
			"side":     p("string", "req | res | both (default both)"),
			"maxBytes": p("integer", "max bytes per side (default 4000)"),
		}, "id"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
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
		"Get a compact, decision-ready summary of a flow: URL/status, notable security headers, query parameters (injection points), passive scanner findings, and whether it's in scope. Use this before get_flow to decide what to inspect.",
		obj(map[string]any{"id": p("integer", "flow id")}, "id"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/analyze", id))
		})

	s.add("flow_as_curl",
		"Render a captured flow's request as a runnable curl command (preserves the exact path; skips TLS verification) so the user can reproduce or iterate on it in a terminal.",
		obj(map[string]any{"id": p("integer", "flow id")}, "id"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/curl", id))
		})

	s.add("send_request",
		"Send a request directly to a target (Repeater) and record it. Returns the resulting flow id+status; call get_flow with that id to read the response body.",
		obj(map[string]any{
			"method":  p("string", "HTTP method"),
			"url":     p("string", "absolute URL"),
			"headers": p("string", "raw header lines 'Key: Value', one per line"),
			"body":    p("string", "request body"),
		}, "url"),
		func(a map[string]any) (string, error) {
			out, err := s.api(http.MethodPost, "/api/repeater/send", map[string]any{
				"method": argStr(a, "method"), "url": argStr(a, "url"),
				"headers": argStr(a, "headers"), "body": argStr(a, "body"),
			})
			if err != nil {
				return "", err
			}
			return out + "\n(call get_flow with this id for the full response)", nil
		})

	s.add("start_intruder",
		"Start a payload attack. Wrap fuzz points in §…§ in the template. attackType: sniper (one position at a time) or pitchfork (lists in parallel). payloads: a list of lists (one list for sniper; one per position for pitchfork).",
		obj(map[string]any{
			"target":     p("string", "scheme://host[:port]"),
			"template":   p("string", "raw request with §…§ fuzz points"),
			"attackType": p("string", "sniper | pitchfork"),
			"payloads":   map[string]any{"type": "array", "description": "list of payload lists", "items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		}, "target", "template"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intruder/start", map[string]any{
				"target": argStr(a, "target"), "template": argStr(a, "template"),
				"attackType": argStr(a, "attackType"), "payloads": a["payloads"],
			})
		})

	s.add("intruder_state",
		"Get the current/last Intruder attack's progress and results (status/length/time per payload; anomalies flagged).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet("/api/intruder/state")
			return truncate(out, 12000), err
		})

	s.add("run_scanner",
		"Run the passive scanner over captured flows and return the findings (severity/title/target/evidence/fix).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/scanner/run", nil) })

	s.add("list_issues", "List current scanner findings.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/scanner/issues") })

	s.add("scan_report",
		"Get the current passive-scan findings as a formatted Markdown report grouped by severity — ready to drop into a writeup.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/scanner/report") })

	s.add("list_checks",
		"List the custom Starlark scanner checks (id, source, and any compile error). These run on every scan alongside the built-ins.",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/checks") })

	s.add("test_check",
		"Compile and run a custom check's Starlark source against a captured flow WITHOUT saving it — returns the findings, or the compile/runtime error. Iterate here until it's right, then save_check. Omit flowId to test against the most recent flow. A check is `def check(flow): return [finding(severity,title,detail=,evidence=,fix=)]` — flow exposes method/scheme/host/port/path/status/mime, req_body/res_body, req_header(name)/res_header(name), query_param(name); builtin re_search(pattern,text).",
		obj(map[string]any{
			"source": p("string", "the check's Starlark source"),
			"flowId": p("integer", "flow to test against (optional; default = most recent)"),
		}, "source"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/checks/test", map[string]any{"source": argStr(a, "source"), "flowId": argInt(a, "flowId", 0)})
		})

	s.add("save_check",
		"Create or update a custom Starlark scanner check by id (letters/digits/-/_). The source must compile or it is rejected. Once saved it runs on every scan. Use test_check first.",
		obj(map[string]any{
			"id":     p("string", "check id / filename stem"),
			"source": p("string", "the check's Starlark source"),
		}, "id", "source"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPut, "/api/checks/"+url.PathEscape(argStr(a, "id")), map[string]any{"source": argStr(a, "source")})
		})

	s.add("delete_check", "Delete a custom scanner check by id.",
		obj(map[string]any{"id": p("string", "check id")}, "id"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodDelete, "/api/checks/"+url.PathEscape(argStr(a, "id")), nil)
		})

	s.add("get_intercept", "Get intercept state and the current hold queue.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/intercept") })

	s.add("set_intercept", "Enable or disable request interception.",
		obj(map[string]any{"enabled": p("boolean", "true to hold requests")}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intercept/toggle", map[string]any{"enabled": argBool(a, "enabled", false)})
		})

	s.add("set_response_intercept", "Enable or disable response interception (hold responses to edit/drop before they reach the client).",
		obj(map[string]any{"enabled": p("boolean", "true to hold responses")}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/intercept/response/toggle", map[string]any{"enabled": argBool(a, "enabled", false)})
		})

	s.add("forward_request", "Forward a held request (optionally replacing it with edited raw bytes).",
		obj(map[string]any{"id": p("integer", "held request id"), "raw": p("string", "optional edited raw request")}, "id"),
		func(a map[string]any) (string, error) {
			body := map[string]any{}
			if r := argStr(a, "raw"); r != "" {
				body["raw"] = r
			}
			return s.api(http.MethodPost, fmt.Sprintf("/api/intercept/%d/forward", argInt(a, "id", 0)), body)
		})

	s.add("drop_request", "Drop a held request.", obj(map[string]any{"id": p("integer", "held request id")}, "id"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, fmt.Sprintf("/api/intercept/%d/drop", argInt(a, "id", 0)), nil)
		})

	s.add("list_rules", "List match-&-replace rules.", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/rules") })

	s.add("add_rule", "Add a request-side match-&-replace rule (regex).",
		obj(map[string]any{
			"type":    p("string", "req-header | req-body"),
			"match":   p("string", "regex to match"),
			"replace": p("string", "replacement"),
			"enabled": p("boolean", "default true"),
		}, "type", "match"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/rules", map[string]any{
				"type": argStr(a, "type"), "match": argStr(a, "match"),
				"replace": argStr(a, "replace"), "enabled": argBool(a, "enabled", true),
			})
		})

	s.add("list_ws_frames", "List captured WebSocket frames for a flow (direction/opcode/length/preview).",
		obj(map[string]any{"id": p("integer", "websocket flow id")}, "id"),
		func(a map[string]any) (string, error) {
			out, err := s.apiGet(fmt.Sprintf("/api/flows/%d/ws", argInt(a, "id", 0)))
			return truncate(out, 12000), err
		})

	s.add("ws_send",
		"WebSocket Repeater: open a fresh WebSocket to a target, send one message, and return the frames the server replies with. url is ws:// or wss://. Optionally send a binary frame, or pass extra handshake headers (e.g. a Cookie) as 'Key: Value' lines.",
		obj(map[string]any{
			"url":     p("string", "ws:// or wss:// target URL"),
			"message": p("string", "message payload to send"),
			"binary":  p("boolean", "send a binary frame instead of text"),
			"headers": p("string", "extra handshake header lines 'Key: Value' (optional)"),
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
		"Add a target-scope rule. action: include | exclude. host supports a leading wildcard (e.g. *.acme.com); path is a prefix. Scope focuses the history, intercept, and scanner.",
		obj(map[string]any{
			"action":  p("string", "include | exclude"),
			"host":    p("string", "host pattern, e.g. *.acme.com"),
			"path":    p("string", "path prefix (optional)"),
			"scheme":  p("string", "http | https (optional)"),
			"enabled": p("boolean", "default true"),
		}, "action"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/scope", map[string]any{
				"action": argStr(a, "action"), "host": argStr(a, "host"), "path": argStr(a, "path"),
				"scheme": argStr(a, "scheme"), "enabled": argBool(a, "enabled", true),
			})
		})

	s.add("get_settings", "Get proxy/intercept settings (proxy bind address, intercept on/off).", obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/settings") })

	s.add("set_session",
		"Set session/auth headers auto-applied to every Repeater/Intruder send (e.g. an Authorization bearer token or a Cookie), so your requests stay authenticated without re-pasting credentials. Provide headers as 'Key: Value' lines; set enabled=false to stop applying them.",
		obj(map[string]any{
			"enabled": p("boolean", "true to apply the headers to outgoing sends"),
			"headers": p("string", "header lines 'Key: Value', one per line (e.g. 'Authorization: Bearer …')"),
		}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/session", map[string]any{
				"enabled": argBool(a, "enabled", false),
				"headers": argStr(a, "headers"),
			})
		})

	s.add("active_scan",
		"ACTIVE scan: send crafted attack payloads to confirm vulnerabilities (reflected XSS, SQLi, SSTI, open redirect, path traversal, timing OS command injection) on an in-scope endpoint. This sends real attack traffic — only run against targets you're authorized to test. Pass arm=true to confirm authorization and enable scanning (a session-level gate). Target a single flow with flowId, or set inScope=true to scan all in-scope endpoints. Returns immediately; poll active_scan_state for progress + confirmed findings.",
		obj(map[string]any{
			"arm":         p("boolean", "true to confirm you're authorized and enable active scanning"),
			"flowId":      p("integer", "scan one captured flow's endpoint"),
			"inScope":     p("boolean", "scan all in-scope endpoints with injectable params"),
			"maxRequests": p("integer", "total probe budget (default 2000)"),
		}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/activescan/start", map[string]any{
				"arm": argBool(a, "arm", false), "flowId": argInt(a, "flowId", 0),
				"inScope": argBool(a, "inScope", false), "maxRequests": argInt(a, "maxRequests", 0),
			})
		})

	s.add("active_scan_state", "Active-scan progress + confirmed findings (armed/running/targets/requests).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.apiGet("/api/activescan") })

	s.add("active_scan_stop", "Stop the running active scan (kill switch).",
		obj(map[string]any{}),
		func(a map[string]any) (string, error) { return s.api(http.MethodPost, "/api/activescan/stop", nil) })

	s.add("decode",
		"Decode or encode a string. op: base64encode/base64decode, urlencode/urldecode, hexencode/hexdecode, htmlencode/htmldecode, jwtdecode (inspect a JWT's header+payload), or smart (auto-detect and decode one layer).",
		obj(map[string]any{
			"op":    p("string", "the transform to apply"),
			"input": p("string", "the string to transform"),
		}, "op", "input"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/decode", map[string]any{"op": argStr(a, "op"), "input": argStr(a, "input")})
		})

	s.add("ca_info", "How to trust the CA so HTTPS can be intercepted (proxy address + CA location).", obj(map[string]any{}),
		func(a map[string]any) (string, error) {
			settings, _ := s.apiGet("/api/settings")
			return fmt.Sprintf("To intercept HTTPS, point the client at the proxy and trust the local CA.\nSettings: %s\nCA download: %s/api/ca.crt (also at ~/.interceptor/ca/ca.crt). Install and trust it on the client.", strings.TrimSpace(settings), s.base), nil
		})
}
