package aiassist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Tool is one callable function exposed to the model during agent mode.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ContentBlock is one piece of an agent message (text, tool_use, or tool_result).
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// ToolCall is a tool invocation the model requested.
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

// AgentMessage is one turn in an agent conversation (user or assistant).
type AgentMessage struct {
	Role    string
	Content any // string or []ContentBlock
}

// AgentTurn is one non-streaming model reply that may include tool calls.
type AgentTurn struct {
	StopReason string
	Blocks     []ContentBlock
	ToolCalls  []ToolCall
	Text       string
}

// usesAnthropicWire reports whether the provider speaks the Anthropic Messages
// wire format (native Anthropic, plus GLM's Anthropic-compatible endpoint).
func (c *Client) usesAnthropicWire() bool {
	return c.provider == ProviderAnthropic || c.provider == ProviderGLM
}

// usesOpenAIWire reports whether the provider speaks the OpenAI chat-completions
// wire format (native OpenAI plus OpenRouter, which fronts it).
func (c *Client) usesOpenAIWire() bool {
	return c.provider == ProviderOpenRouter || c.provider == ProviderOpenAI
}

// SupportsAgentTools reports whether this client can run tool-use agent loops.
// Every supported provider now has a tool-calling path: Anthropic/GLM via the
// Anthropic Messages format, OpenRouter/OpenAI via chat-completions tool_calls.
func (c *Client) SupportsAgentTools() bool {
	return c.usesAnthropicWire() || c.usesOpenAIWire()
}

// Provider returns the resolved provider constant for this client.
func (c *Client) Provider() string { return c.provider }

// CompleteAgentTurn sends one agent turn with tools and returns the model reply.
// It dispatches to the Anthropic wire format (Anthropic/GLM) or the OpenAI
// chat-completions format (OpenRouter/OpenAI) based on the configured provider.
func (c *Client) CompleteAgentTurn(ctx context.Context, system string, messages []AgentMessage, tools []Tool) (*AgentTurn, error) {
	if c.key == "" {
		return nil, fmt.Errorf("no API key configured")
	}
	switch {
	case c.usesAnthropicWire():
		return c.completeAnthropicAgentTurn(ctx, system, messages, tools)
	case c.usesOpenAIWire():
		return c.completeOpenAIAgentTurn(ctx, system, messages, tools)
	default:
		return nil, fmt.Errorf("agent mode not supported for provider %s", c.provider)
	}
}

func (c *Client) completeAnthropicAgentTurn(ctx context.Context, system string, messages []AgentMessage, tools []Tool) (*AgentTurn, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": agentMaxTokens,
		"system":     system,
		"messages":   agentMessageMaps(messages),
		"tools":      anthropicTools(tools),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setAnthropicAuth(req)
	raw, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return parseAgentTurn(raw)
}

// CompleteStreamAgentMessages streams a final agent reply (no tools) token by token.
func (c *Client) CompleteStreamAgentMessages(ctx context.Context, system string, messages []AgentMessage, onDelta func(string)) error {
	if c.key == "" {
		return fmt.Errorf("no API key configured")
	}
	switch {
	case c.usesAnthropicWire():
		body, _ := json.Marshal(map[string]any{
			"model":      c.model,
			"max_tokens": agentMaxTokens,
			"stream":     true,
			"system":     system,
			"messages":   agentMessageMaps(messages),
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		c.setAnthropicAuth(req)
		return c.stream(req, onDelta, anthropicDelta)
	case c.usesOpenAIWire():
		// Reuse the plain chat streaming path — the final synthesis turn carries
		// no tools, so a text-only stream over the flattened history suffices.
		return c.streamOpenRouterMessages(ctx, system, flattenAgentMessages(messages), onDelta)
	default:
		return fmt.Errorf("agent mode not supported for provider %s", c.provider)
	}
}

// ---- OpenAI / OpenRouter chat-completions tool calling ----

// completeOpenAIAgentTurn runs one agent turn over the OpenAI chat-completions
// `tools`/`tool_calls` format and normalizes the reply back into an AgentTurn so
// callers stay wire-format agnostic.
func (c *Client) completeOpenAIAgentTurn(ctx context.Context, system string, messages []AgentMessage, tools []Tool) (*AgentTurn, error) {
	msgs := []map[string]any{{"role": "system", "content": system}}
	msgs = append(msgs, openAIAgentMessages(messages)...)
	payload := map[string]any{
		"model":      c.model,
		"max_tokens": agentMaxTokens,
		"messages":   msgs,
	}
	if len(tools) > 0 {
		payload["tools"] = openAITools(tools)
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/Veyal/interseptor")
	req.Header.Set("X-Title", "Interseptor")
	raw, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return parseOpenAIAgentTurn(raw)
}

// openAITools maps our Tool specs to the OpenAI `tools` array shape.
func openAITools(tools []Tool) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return out
}

// openAIAgentMessages flattens our Anthropic-style AgentMessages (whose content
// may be a []ContentBlock of text/tool_use/tool_result) into the OpenAI chat
// schema: assistant messages carry a `tool_calls` array, and each tool_result
// becomes a separate message with role "tool" and a matching tool_call_id.
func openAIAgentMessages(messages []AgentMessage) []map[string]any {
	var out []map[string]any
	for _, m := range messages {
		switch content := m.Content.(type) {
		case string:
			out = append(out, map[string]any{"role": m.Role, "content": content})
		case []ContentBlock:
			var text string
			var toolCalls []map[string]any
			var toolResults []map[string]any
			for _, b := range content {
				switch b.Type {
				case "text":
					text += b.Text
				case "tool_use":
					args := []byte("{}")
					if len(b.Input) > 0 {
						if m, err := json.Marshal(b.Input); err == nil && len(m) > 0 {
							args = m
						}
					}
					toolCalls = append(toolCalls, map[string]any{
						"id":   b.ID,
						"type": "function",
						"function": map[string]any{
							"name":      b.Name,
							"arguments": string(args),
						},
					})
				case "tool_result":
					toolResults = append(toolResults, map[string]any{
						"role":         "tool",
						"tool_call_id": b.ToolUseID,
						"content":      b.Content,
					})
				}
			}
			if m.Role == "assistant" {
				msg := map[string]any{"role": "assistant"}
				if text != "" {
					msg["content"] = text
				} else {
					msg["content"] = nil
				}
				if len(toolCalls) > 0 {
					msg["tool_calls"] = toolCalls
				}
				out = append(out, msg)
			} else {
				// User-side content blocks are tool results (and/or text).
				if text != "" {
					out = append(out, map[string]any{"role": "user", "content": text})
				}
				out = append(out, toolResults...)
			}
		default:
			out = append(out, map[string]any{"role": m.Role, "content": fmt.Sprint(content)})
		}
	}
	return out
}

// flattenAgentMessages collapses AgentMessages into plain text Messages for the
// tool-less final synthesis stream (tool_use/tool_result blocks are summarized).
func flattenAgentMessages(messages []AgentMessage) []Message {
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		switch content := m.Content.(type) {
		case string:
			out = append(out, Message{Role: m.Role, Content: content})
		case []ContentBlock:
			var sb strings.Builder
			for _, b := range content {
				switch b.Type {
				case "text":
					sb.WriteString(b.Text)
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					sb.WriteString(fmt.Sprintf("[called %s %s]", b.Name, string(args)))
				case "tool_result":
					sb.WriteString(fmt.Sprintf("[tool result: %s]", b.Content))
				}
			}
			role := m.Role
			if role == "" {
				role = "user"
			}
			out = append(out, Message{Role: role, Content: sb.String()})
		}
	}
	return out
}

// parseOpenAIAgentTurn decodes an OpenAI chat-completions reply into an AgentTurn,
// mapping tool_calls (with JSON-string arguments) into our ToolCall shape.
func parseOpenAIAgentTurn(raw []byte) (*AgentTurn, error) {
	var cr struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("ai response: %s", string(raw))
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("ai: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("ai: empty response")
	}
	ch := cr.Choices[0]
	turn := &AgentTurn{StopReason: ch.FinishReason, Text: ch.Message.Content}
	if ch.Message.Content != "" {
		turn.Blocks = append(turn.Blocks, ContentBlock{Type: "text", Text: ch.Message.Content})
	}
	for _, tc := range ch.Message.ToolCalls {
		var input map[string]any
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		if input == nil {
			input = map[string]any{}
		}
		turn.Blocks = append(turn.Blocks, ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
		turn.ToolCalls = append(turn.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	return turn, nil
}

func anthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
	}
	return out
}

func agentMessageMaps(messages []AgentMessage) []map[string]any {
	out := make([]map[string]any, len(messages))
	for i, m := range messages {
		out[i] = map[string]any{"role": m.Role, "content": m.Content}
	}
	return out
}

func parseAgentTurn(raw []byte) (*AgentTurn, error) {
	var mr struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &mr); err != nil {
		return nil, fmt.Errorf("ai response: %s", string(raw))
	}
	if mr.Error != nil {
		return nil, fmt.Errorf("ai: %s", mr.Error.Message)
	}
	turn := &AgentTurn{StopReason: mr.StopReason}
	for _, p := range mr.Content {
		switch p.Type {
		case "text":
			turn.Blocks = append(turn.Blocks, ContentBlock{Type: "text", Text: p.Text})
			turn.Text += p.Text
		case "tool_use":
			turn.Blocks = append(turn.Blocks, ContentBlock{Type: "tool_use", ID: p.ID, Name: p.Name, Input: p.Input})
			turn.ToolCalls = append(turn.ToolCalls, ToolCall{ID: p.ID, Name: p.Name, Input: p.Input})
		}
	}
	return turn, nil
}

