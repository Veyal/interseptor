package aiagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/aiassist"
)

func TestClientToolCallerAnthropicWire(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"stop_reason":"tool_use","content":[{"type":"text","text":"probing"},{"type":"tool_use","id":"tu_1","name":"send_request","input":{"url":"https://x.test/"}}]}`)
	}))
	defer srv.Close()

	c := aiassist.New(aiassist.ProviderAnthropic, "sk", "", srv.URL)
	tc, err := NewClientToolCaller(c)
	if err != nil {
		t.Fatalf("NewClientToolCaller: %v", err)
	}
	turn, err := tc.Complete(context.Background(), "sys",
		[]Message{{Role: "user", Text: "check access"}},
		[]ToolSpec{{Name: "send_request", Description: "send", Schema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if turn.Text != "probing" || len(turn.Calls) != 1 {
		t.Fatalf("unexpected turn: %+v", turn)
	}
	if turn.Calls[0].Name != "send_request" || turn.Calls[0].Args["url"] != "https://x.test/" {
		t.Fatalf("tool call wrong: %+v", turn.Calls[0])
	}
	// Anthropic tools use input_schema.
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", gotBody["tools"])
	}
}

func TestClientToolCallerOpenAIWire(t *testing.T) {
	var gotMsgs []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		gotMsgs, _ = body["messages"].([]any)
		io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"content":"looking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_flow","arguments":"{\"id\":7}"}}]}}]}`)
	}))
	defer srv.Close()

	c := aiassist.New(aiassist.ProviderOpenAI, "sk", "", srv.URL)
	tc, err := NewClientToolCaller(c)
	if err != nil {
		t.Fatalf("NewClientToolCaller: %v", err)
	}
	// Include an assistant tool_use + a tool result to exercise message conversion.
	msgs := []Message{
		{Role: "user", Text: "start"},
		{Role: "assistant", Text: "", Calls: []ToolCall{{ID: "call_0", Name: "get_flow", Args: map[string]any{"id": 1}}}},
		{Role: "tool", ToolCallID: "call_0", Text: "prior body"},
	}
	turn, err := tc.Complete(context.Background(), "sys", msgs,
		[]ToolSpec{{Name: "get_flow", Description: "read", Schema: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(turn.Calls) != 1 || turn.Calls[0].Name != "get_flow" {
		t.Fatalf("unexpected calls: %+v", turn.Calls)
	}
	if v, _ := turn.Calls[0].Args["id"].(float64); v != 7 {
		t.Fatalf("arg id=%v want 7", turn.Calls[0].Args["id"])
	}
	// system + user + assistant(tool_calls) + tool result = 4.
	if len(gotMsgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(gotMsgs), gotMsgs)
	}
	toolMsg, _ := gotMsgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_0" {
		t.Fatalf("tool result message wrong: %v", toolMsg)
	}
}

func TestNewClientToolCallerNil(t *testing.T) {
	if _, err := NewClientToolCaller(nil); err == nil {
		t.Fatal("expected error for nil client")
	}
}
