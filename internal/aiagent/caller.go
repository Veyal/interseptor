package aiagent

import (
	"context"
	"fmt"

	"github.com/Veyal/interseptor/internal/aiassist"
)

// ErrNoToolSupport is returned when the configured provider has no tool-calling
// path at all (so an agent run cannot proceed).
type ErrNoToolSupport struct{ Provider string }

func (e ErrNoToolSupport) Error() string {
	return fmt.Sprintf("provider %q does not support tool-calling agent mode", e.Provider)
}

// clientToolCaller adapts an *aiassist.Client into a ToolCaller. It reuses the
// client's CompleteAgentTurn, which already dispatches to the correct wire format
// (Anthropic/GLM vs OpenAI/OpenRouter) internally, so this adapter only translates
// between the aiagent-normalized types and aiassist's AgentMessage/AgentTurn types.
type clientToolCaller struct{ c *aiassist.Client }

// NewClientToolCaller wraps an *aiassist.Client as a ToolCaller. It returns
// ErrNoToolSupport if the client's provider cannot run tool-calling loops.
func NewClientToolCaller(c *aiassist.Client) (ToolCaller, error) {
	if c == nil {
		return nil, fmt.Errorf("aiagent: nil client")
	}
	if !c.SupportsAgentTools() {
		return nil, ErrNoToolSupport{Provider: c.Provider()}
	}
	return clientToolCaller{c: c}, nil
}

func (t clientToolCaller) Complete(ctx context.Context, system string, msgs []Message, tools []ToolSpec) (Turn, error) {
	turn, err := t.c.CompleteAgentTurn(ctx, system, toAgentMessages(msgs), toAssistTools(tools))
	if err != nil {
		return Turn{}, err
	}
	out := Turn{Text: turn.Text}
	for _, tc := range turn.ToolCalls {
		out.Calls = append(out.Calls, ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Input})
	}
	return out, nil
}

// toAssistTools converts normalized ToolSpecs to aiassist.Tool.
func toAssistTools(tools []ToolSpec) []aiassist.Tool {
	out := make([]aiassist.Tool, len(tools))
	for i, t := range tools {
		schema := t.Schema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out[i] = aiassist.Tool{Name: t.Name, Description: t.Description, InputSchema: schema}
	}
	return out
}

// toAgentMessages rebuilds aiassist.AgentMessages (the Anthropic-style content-block
// representation both wire formats consume) from the flat aiagent Message history.
//   - user/plain-text  -> {role, content:string}
//   - assistant + calls -> {role:"assistant", content:[]{text?, tool_use...}}
//   - tool result       -> {role:"user",      content:[]{tool_result...}}
func toAgentMessages(msgs []Message) []aiassist.AgentMessage {
	out := make([]aiassist.AgentMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if len(m.Calls) == 0 {
				out = append(out, aiassist.AgentMessage{Role: "assistant", Content: m.Text})
				continue
			}
			blocks := make([]aiassist.ContentBlock, 0, len(m.Calls)+1)
			if m.Text != "" {
				blocks = append(blocks, aiassist.ContentBlock{Type: "text", Text: m.Text})
			}
			for _, c := range m.Calls {
				blocks = append(blocks, aiassist.ContentBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Args})
			}
			out = append(out, aiassist.AgentMessage{Role: "assistant", Content: blocks})
		case "tool":
			out = append(out, aiassist.AgentMessage{Role: "user", Content: []aiassist.ContentBlock{
				{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Text},
			}})
		default: // "user" and anything else -> plain text
			out = append(out, aiassist.AgentMessage{Role: "user", Content: m.Text})
		}
	}
	return out
}
