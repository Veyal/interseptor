package aiassist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// SupportsAgentTools reports whether this client can run tool-use agent loops.
func (c *Client) SupportsAgentTools() bool {
	return c.provider == ProviderAnthropic
}

// CompleteAgentTurn sends one agent turn with tools and returns the model reply.
func (c *Client) CompleteAgentTurn(ctx context.Context, system string, messages []AgentMessage, tools []Tool) (*AgentTurn, error) {
	if c.key == "" {
		return nil, fmt.Errorf("no API key configured")
	}
	if c.provider != ProviderAnthropic {
		return nil, fmt.Errorf("agent mode requires Anthropic provider (not %s)", c.provider)
	}
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   agentMessageMaps(messages),
		"tools":      anthropicTools(tools),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
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
	if c.provider != ProviderAnthropic {
		return fmt.Errorf("agent mode requires Anthropic provider (not %s)", c.provider)
	}
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"stream":     true,
		"system":     system,
		"messages":   agentMessageMaps(messages),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	return c.stream(req, onDelta, anthropicDelta)
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

