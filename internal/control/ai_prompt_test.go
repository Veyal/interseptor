package control

import (
	"strings"
	"testing"
)

// assistPrompt builds the AI-assist user prompt. A single flow keeps the focused
// wording; multiple selected flows become a combined, per-endpoint review.
func TestAssistPrompt(t *testing.T) {
	flows := []assistFlow{
		{Label: "#1 GET https://h/a", Req: "GET /a", Res: "200 ok"},
		{Label: "#2 POST https://h/b", Req: "POST /b", Res: "500 err"},
	}

	multi := assistPrompt("explain", flows, "")
	for _, want := range []string{"2 exchanges", "#1 GET https://h/a", "#2 POST https://h/b", "GET /a", "POST /b"} {
		if !strings.Contains(multi, want) {
			t.Fatalf("multi-flow prompt missing %q:\n%s", want, multi)
		}
	}

	single := assistPrompt("explain", flows[:1], "")
	if !strings.Contains(single, "Explain what this HTTP request/response does") {
		t.Fatalf("single-flow prompt lost its focused wording:\n%s", single)
	}

	// "ask" weaves the free-text question into the prompt (single and multi).
	ask := assistPrompt("ask", flows[:1], "Is the CSRF token validated?")
	if !strings.Contains(ask, "Is the CSRF token validated?") || !strings.Contains(ask, "GET /a") {
		t.Fatalf("ask prompt missing the question or the exchange:\n%s", ask)
	}
	askMulti := assistPrompt("ask", flows, "Which endpoint is riskiest?")
	if !strings.Contains(askMulti, "Which endpoint is riskiest?") || !strings.Contains(askMulti, "2 exchanges") {
		t.Fatalf("ask multi prompt missing question/exchanges:\n%s", askMulti)
	}
}

func TestBuildAskMessagesFollowUp(t *testing.T) {
	flows := []assistFlow{{Label: "#1 GET https://h/a", Req: "GET /a", Res: "200 ok"}}
	history := []aiAssistTurn{
		{Role: "user", Content: "Is CSRF validated?"},
		{Role: "assistant", Content: "No CSRF token in the request."},
	}
	msgs := buildAskMessages(flows, history, "What header should carry it?")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (context + 2 history + question), got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "GET /a") || !strings.Contains(msgs[0].Content, "follow-up") {
		t.Fatalf("context message missing exchange:\n%s", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "Is CSRF validated?" {
		t.Fatalf("history user turn wrong: %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" {
		t.Fatalf("history assistant turn wrong: %+v", msgs[2])
	}
	if msgs[3].Content != "What header should carry it?" {
		t.Fatalf("follow-up question wrong: %q", msgs[3].Content)
	}

	first := buildAskMessages(flows, nil, "Is CSRF validated?")
	if len(first) != 1 || !strings.Contains(first[0].Content, "Is CSRF validated?") {
		t.Fatalf("first ask should be a single combined prompt:\n%+v", first)
	}
}

func TestExtractJSONArray(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[{"a":1}]`, `[{"a":1}]`},
		{"```json\n[{\"a\":1}]\n```", `[{"a":1}]`},
		{`Here are the payloads:\n[1,2,3]\nHope that helps!`, `[1,2,3]`},
		{`no array here`, ``},
		{``, ``},
	}
	for _, c := range cases {
		if got := extractJSONArray(c.in); got != c.want {
			t.Fatalf("extractJSONArray(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
