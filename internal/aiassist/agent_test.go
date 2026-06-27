package aiassist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupportsAgentToolsAnthropicOnly(t *testing.T) {
	if !New(ProviderAnthropic, "k", "").SupportsAgentTools() {
		t.Fatal("Anthropic should support agent tools")
	}
	if New(ProviderOpenRouter, "k", "").SupportsAgentTools() {
		t.Fatal("OpenRouter should not support agent tools in MVP")
	}
}

func TestCompleteAgentTurnParsesToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("expected 1 tool, got %v", body["tools"])
		}
		io.WriteString(w, `{"stop_reason":"tool_use","content":[{"type":"text","text":"Probing."},{"type":"tool_use","id":"tu_1","name":"send_request","input":{"method":"GET","url":"https://x.test/"}}]}`)
	}))
	defer srv.Close()

	c := New(ProviderAnthropic, "sk-test", "")
	c.endpoint = srv.URL
	turn, err := c.CompleteAgentTurn(context.Background(), "sys", []AgentMessage{
		{Role: "user", Content: "check access"},
	}, []Tool{{Name: "send_request", Description: "send", InputSchema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatalf("CompleteAgentTurn: %v", err)
	}
	if turn.StopReason != "tool_use" || len(turn.ToolCalls) != 1 {
		t.Fatalf("unexpected turn: %+v", turn)
	}
	if turn.ToolCalls[0].Name != "send_request" || turn.ToolCalls[0].Input["url"] != "https://x.test/" {
		t.Fatalf("tool call wrong: %+v", turn.ToolCalls[0])
	}
	if turn.Text != "Probing." {
		t.Fatalf("text=%q", turn.Text)
	}
}

func TestCompleteAgentTurnRejectsOpenRouter(t *testing.T) {
	c := New(ProviderOpenRouter, "k", "")
	if _, err := c.CompleteAgentTurn(context.Background(), "s", nil, nil); err == nil || !strings.Contains(err.Error(), "Anthropic") {
		t.Fatalf("expected Anthropic-only error, got %v", err)
	}
}

func TestCompleteAgentTurnEndTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"No IDOR."}]}`)
	}))
	defer srv.Close()

	c := New(ProviderAnthropic, "sk-test", "")
	c.endpoint = srv.URL
	turn, err := c.CompleteAgentTurn(context.Background(), "sys", []AgentMessage{{Role: "user", Content: "q"}}, agentToolsFixture())
	if err != nil {
		t.Fatalf("CompleteAgentTurn: %v", err)
	}
	if len(turn.ToolCalls) != 0 || turn.Text != "No IDOR." {
		t.Fatalf("unexpected turn: %+v", turn)
	}
}

func TestCompleteStreamAgentMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		if body["stream"] != true {
			t.Fatalf("expected stream:true, got %v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Done."}}`+"\n\n")
	}))
	defer srv.Close()

	c := New(ProviderAnthropic, "sk-test", "")
	c.endpoint = srv.URL
	var got strings.Builder
	err := c.CompleteStreamAgentMessages(context.Background(), "sys", []AgentMessage{{Role: "user", Content: "q"}}, func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("CompleteStreamAgentMessages: %v", err)
	}
	if got.String() != "Done." {
		t.Fatalf("got %q", got.String())
	}
}

func agentToolsFixture() []Tool {
	return []Tool{{Name: "get_flow", Description: "read", InputSchema: map[string]any{"type": "object"}}}
}
