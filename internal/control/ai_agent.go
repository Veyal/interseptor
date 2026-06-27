package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

const maxAgentToolSteps = 5

// assistAgentSystem tells the model it may probe via send_request / get_flow when
// the user has opted into agent mode.
const assistAgentSystem = "Concise web-app security testing assistant for a pentester. Answer in at most ~150 words or 6 short Markdown bullets, lead with the most security-relevant point, and skip all preamble, disclaimers, and restating the request. When you need to verify access, IDOR, or response behavior, use send_request to probe URLs and get_flow to read captured or sent flows — reuse the session context from the selected flow (cookies/auth are merged automatically). After at most a few probes, give your final answer."

// agentToolEvent is streamed to the browser as `event: tool`.
type agentToolEvent struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	OK      bool   `json:"ok"`
	Result  string `json:"result"`
}

var agentTools = []aiassist.Tool{
	{
		Name:        "send_request",
		Description: "Send an HTTP request via Repeater and record the flow. Returns flow id and status; call get_flow for the body.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":  map[string]any{"type": "string", "description": "HTTP method (default GET)"},
				"url":     map[string]any{"type": "string", "description": "Absolute URL to request"},
				"headers": map[string]any{"type": "string", "description": "Optional raw Key: Value header lines"},
				"body":    map[string]any{"type": "string", "description": "Optional request body"},
			},
			"required": []string{"url"},
		},
	},
	{
		Name:        "get_flow",
		Description: "Read a flow's raw request and/or response (headers + body).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":       map[string]any{"type": "integer", "description": "Flow id"},
				"side":     map[string]any{"type": "string", "description": "req | res | both (default both)"},
				"maxBytes": map[string]any{"type": "integer", "description": "Max bytes per side (default 4000)"},
			},
			"required": []string{"id"},
		},
	},
}

// aiAssistAgentStream runs the tool-use loop, emits tool SSE events, then streams
// the final answer.
func (h *Hub) aiAssistAgentStream(w http.ResponseWriter, r *http.Request, in aiAssistReq, flows []assistFlow, provider, key, model string, flusher http.Flusher) {
	client := aiassist.New(provider, key, model)
	if !client.SupportsAgentTools() {
		b, _ := json.Marshal("agent mode requires Anthropic provider — switch in Settings → AI assist")
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
		flusher.Flush()
		return
	}

	seedFlow, _ := h.st.GetFlow(in.ids()[0])
	msgs := buildAgentAskMessages(flows, in.History, in.Question)
	ctx := r.Context()

	emitTool := func(ev agentToolEvent) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: tool\ndata: %s\n\n", b)
		flusher.Flush()
	}
	emitText := func(delta string) {
		b, _ := json.Marshal(delta)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	steps := 0
	for steps < maxAgentToolSteps {
		turn, err := client.CompleteAgentTurn(ctx, assistAgentSystem, msgs, agentTools)
		if err != nil {
			b, _ := json.Marshal(err.Error())
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
			flusher.Flush()
			return
		}
		msgs = append(msgs, aiassist.AgentMessage{Role: "assistant", Content: turn.Blocks})

		if len(turn.ToolCalls) == 0 {
			streamAgentText(w, flusher, turn.Text)
			fmt.Fprint(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return
		}

		for _, tc := range turn.ToolCalls {
			result, summary, ok := h.execAgentTool(tc, seedFlow)
			emitTool(agentToolEvent{Tool: tc.Name, Summary: summary, OK: ok, Result: clip(result, 500)})
			msgs = append(msgs, aiassist.AgentMessage{Role: "user", Content: []aiassist.ContentBlock{
				{Type: "tool_result", ToolUseID: tc.ID, Content: result, IsError: !ok},
			}})
		}
		steps++
	}

	// Tool budget exhausted — stream a final synthesis from the full thread.
	err := client.CompleteStreamAgentMessages(ctx, assistAgentSystem, msgs, emitText)
	if err != nil {
		b, _ := json.Marshal(err.Error())
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
	} else {
		fmt.Fprint(w, "event: done\ndata: {}\n\n")
	}
	flusher.Flush()
}

// streamAgentText emits pre-generated text as SSE data chunks (word-sized).
func streamAgentText(w http.ResponseWriter, flusher http.Flusher, text string) {
	if text == "" {
		return
	}
	for _, part := range strings.Fields(text) {
		b, _ := json.Marshal(part + " ")
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
}

func buildAgentAskMessages(flows []assistFlow, history []aiAssistTurn, question string) []aiassist.AgentMessage {
	simple := buildAskMessages(flows, history, question)
	out := make([]aiassist.AgentMessage, len(simple))
	for i, m := range simple {
		out[i] = aiassist.AgentMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

func (h *Hub) execAgentTool(tc aiassist.ToolCall, seed *store.Flow) (result, summary string, ok bool) {
	summary = agentToolSummary(tc.Name, tc.Input)
	switch tc.Name {
	case "send_request":
		return h.agentSendRequest(tc.Input, seed)
	case "get_flow":
		return h.agentGetFlow(tc.Input)
	default:
		return "unknown tool: " + tc.Name, summary, false
	}
}

func (h *Hub) agentSendRequest(args map[string]any, seed *store.Flow) (string, string, bool) {
	url := agentArgStr(args, "url")
	if url == "" {
		return "url is required", agentToolSummary("send_request", args), false
	}
	if h.targetsOwnListener(url) {
		return "refusing to send to Interceptor's own listener", agentToolSummary("send_request", args), false
	}
	method := agentArgStr(args, "method")
	if method == "" {
		method = http.MethodGet
	}
	headers := mergeSeedHeaders(parseHeaderLines(agentArgStr(args, "headers")), seed)
	flow, err := h.snd.Send(sender.Request{
		Method:  method,
		URL:     url,
		Headers: headers,
		Body:    []byte(agentArgStr(args, "body")),
		Flags:   store.FlagRepeater | store.FlagAI,
	})
	if err != nil {
		return err.Error(), agentToolSummary("send_request", args), false
	}
	out := fmt.Sprintf("flow id=%d status=%d %s %s", flow.ID, flow.Status, flow.Method, flow.Path)
	return out, agentToolSummary("send_request", args), true
}

func (h *Hub) agentGetFlow(args map[string]any) (string, string, bool) {
	id := agentArgInt(args, "id", 0)
	if id == 0 {
		return "id is required", agentToolSummary("get_flow", args), false
	}
	f, err := h.st.GetFlow(int64(id))
	if err != nil {
		return err.Error(), agentToolSummary("get_flow", args), false
	}
	max := agentArgInt(args, "maxBytes", 4000)
	side := agentArgStr(args, "side")
	if side == "" {
		side = "both"
	}
	raw := func(sd string) string {
		var b []byte
		switch sd {
		case "req":
			b = h.rawRequest(f)
		case "res":
			b = h.rawResponse(f)
		default:
			return ""
		}
		return clip(string(b), max)
	}
	switch side {
	case "req", "res":
		return raw(side), agentToolSummary("get_flow", args), true
	default:
		out := "=== REQUEST ===\n" + raw("req") + "\n\n=== RESPONSE ===\n" + raw("res")
		return out, agentToolSummary("get_flow", args), true
	}
}

// mergeSeedHeaders fills Cookie, Authorization, and User-Agent from the context
// flow when the model did not supply them.
func mergeSeedHeaders(user map[string][]string, seed *store.Flow) map[string][]string {
	out := make(map[string][]string, len(user))
	for k, v := range user {
		out[k] = append([]string(nil), v...)
	}
	if seed == nil || seed.ReqHeaders == nil {
		return out
	}
	for _, k := range []string{"Cookie", "Authorization", "User-Agent"} {
		if len(out[k]) > 0 {
			continue
		}
		if v, ok := seed.ReqHeaders[k]; ok && len(v) > 0 {
			out[k] = append([]string(nil), v...)
		}
	}
	return out
}

func agentToolSummary(tool string, args map[string]any) string {
	order := []string{"method", "url", "id", "side"}
	var parts []string
	for _, k := range order {
		v := agentArgStr(args, k)
		if v == "" {
			continue
		}
		if len(v) > 60 {
			v = v[:60] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	if len(parts) == 0 {
		return tool
	}
	return strings.Join(parts, " ")
}

func agentArgStr(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func agentArgInt(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		if i, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			return i
		}
		return def
	}
}