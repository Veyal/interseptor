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
			"instructions":    "Interceptor: an intercepting HTTP/HTTPS proxy for web pentesting. Flow: list_flows/analyze_flow to find & triage → get_flow to read → send_request (replay) or start_intruder (fuzz) to attack → run_scanner (passive) or active_scan (sends payloads) to find bugs. Flow ids come from list_flows. Bodies truncate to maxBytes (default 4000). Scanners obey target scope (list_scope/add_scope_rule). active_scan sends real attacks: pass arm=true once to confirm authorization, and it only fires in-scope. Project size: host_stats shows per-host flow/byte breakdown; prune_history deletes noisy hosts (mode=delete) or keeps only the important ones (mode=keepOnly) — destructive, shown live in Activity.",
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
	start := time.Now()
	text, err := t.call(p.Arguments)
	if s.report != nil {
		a := Activity{Tool: p.Name, Summary: activitySummary(p.Name, p.Arguments), OK: err == nil, Ms: time.Since(start).Milliseconds()}
		if err != nil {
			a.Result = firstLine(err.Error(), 160)
		} else {
			a.Result = firstLine(text, 160)
		}
		s.report(a)
	}
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
			"limit":  p("integer", "default 50"),
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
		"Read a flow's raw request and/or response (headers + body).",
		obj(map[string]any{
			"id":       pt("integer"),
			"side":     p("string", "req | res | both (default both)"),
			"maxBytes": pt("integer"),
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
		"Compact triage of a flow: URL/status, security headers, query params (injection points), passive findings, in-scope flag. Cheaper than get_flow for deciding what to inspect.",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/analyze", id))
		})

	s.add("flow_as_curl",
		"Render a flow's request as a runnable curl command.",
		obj(map[string]any{"id": pt("integer")}, "id"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			return s.apiGet(fmt.Sprintf("/api/flows/%d/curl", id))
		})

	s.add("set_note",
		"Annotate a flow with a note (record a finding for the operator; \"\" clears it). Visible in the UI inspector and on get_flow/list_flows.",
		obj(map[string]any{
			"id":   pt("integer"),
			"note": pt("string"),
		}, "id", "note"),
		func(a map[string]any) (string, error) {
			id := argInt(a, "id", 0)
			if id == 0 {
				return "", fmt.Errorf("id is required")
			}
			if _, err := s.api(http.MethodPut, fmt.Sprintf("/api/flows/%d/note", id), map[string]any{"note": argStr(a, "note")}); err != nil {
				return "", err
			}
			return fmt.Sprintf("noted flow %d", id), nil
		})

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

	s.add("send_request",
		"Send an HTTP request (Repeater) and record it. Returns the flow id+status; get_flow that id for the body.",
		obj(map[string]any{
			"method":  pt("string"),
			"url":     p("string", "absolute URL"),
			"headers": p("string", "'Key: Value' lines"),
			"body":    pt("string"),
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
		"Fuzz a request. Mark fuzz points with §…§ in template. attackType sniper=one position at a time, pitchfork=lists in parallel. payloads=list of lists (one for sniper; one per position for pitchfork).",
		obj(map[string]any{
			"target":     p("string", "scheme://host[:port]"),
			"template":   p("string", "raw request with §…§"),
			"attackType": p("string", "sniper | pitchfork"),
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
		"Auth headers auto-applied to every Repeater/Intruder send (e.g. Authorization/Cookie) so sends stay authenticated. enabled=false to stop.",
		obj(map[string]any{
			"enabled": pt("boolean"),
			"headers": p("string", "'Key: Value' lines"),
		}, "enabled"),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/session", map[string]any{
				"enabled": argBool(a, "enabled", false),
				"headers": argStr(a, "headers"),
			})
		})

	s.add("active_scan",
		"ACTIVE scan — sends real attack payloads (reflected XSS, SQLi, SSTI, open redirect, path traversal, timing OS-cmd-injection) to an in-scope target. Authorized targets only. arm=true confirms authorization (session gate, required once). Target one flowId, or inScope=true for all in-scope endpoints. Async — poll active_scan_state.",
		obj(map[string]any{
			"arm":         p("boolean", "confirm authorization + enable"),
			"flowId":      p("integer", "scan one flow's endpoint"),
			"inScope":     p("boolean", "scan all in-scope endpoints"),
			"maxRequests": p("integer", "probe budget (default 2000)"),
		}),
		func(a map[string]any) (string, error) {
			return s.api(http.MethodPost, "/api/activescan/start", map[string]any{
				"arm": argBool(a, "arm", false), "flowId": argInt(a, "flowId", 0),
				"inScope": argBool(a, "inScope", false), "maxRequests": argInt(a, "maxRequests", 0),
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
			return fmt.Sprintf("To intercept HTTPS, point the client at the proxy and trust the local CA.\nSettings: %s\nCA download: %s/api/ca.crt (also at ~/.interceptor/ca/ca.crt). Install and trust it on the client.", strings.TrimSpace(settings), s.base), nil
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
}
