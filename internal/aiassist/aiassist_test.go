package aiassist

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicBuildsRequestAndParsesReply(t *testing.T) {
	var gotKey, gotVersion string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"content":[{"type":"text","text":"This is a login request."}]}`)
	}))
	defer srv.Close()

	c := New(ProviderAnthropic, "sk-test", "")
	c.endpoint = srv.URL
	out, err := c.Complete("you are a security assistant", "explain this request")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "This is a login request." {
		t.Fatalf("unexpected reply: %q", out)
	}
	if gotKey != "sk-test" || gotVersion != "2023-06-01" {
		t.Fatalf("headers wrong: key=%q version=%q", gotKey, gotVersion)
	}
	if gotBody["model"] != defaultAnthropicModel || gotBody["system"] != "you are a security assistant" {
		t.Fatalf("request body wrong: %v", gotBody)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %v", gotBody["messages"])
	}
}

func TestOpenRouterBuildsChatRequestAndParsesReply(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Try a SQLi payload."}}]}`)
	}))
	defer srv.Close()

	c := New(ProviderOpenRouter, "sk-or-test", "openai/gpt-4o-mini")
	c.endpoint = srv.URL
	out, err := c.Complete("you are a security assistant", "suggest a payload")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "Try a SQLi payload." {
		t.Fatalf("unexpected reply: %q", out)
	}
	if gotAuth != "Bearer sk-or-test" {
		t.Fatalf("auth header wrong: %q", gotAuth)
	}
	if gotBody["model"] != "openai/gpt-4o-mini" {
		t.Fatalf("model wrong: %v", gotBody["model"])
	}
	// OpenAI chat format carries the system prompt as the first message.
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %v", gotBody["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first message should be system, got %v", first)
	}
}

func TestOpenRouterDefaultsModel(t *testing.T) {
	c := New(ProviderOpenRouter, "k", "")
	if c.model != defaultOpenRouterModel {
		t.Fatalf("expected default OpenRouter model, got %q", c.model)
	}
	if c.endpoint != openRouterEndpoint {
		t.Fatalf("expected OpenRouter endpoint, got %q", c.endpoint)
	}
}

func TestUnknownProviderFallsBackToAnthropic(t *testing.T) {
	c := New("", "k", "")
	if c.provider != ProviderAnthropic || c.endpoint != anthropicEndpoint {
		t.Fatalf("empty provider should default to anthropic, got %q %q", c.provider, c.endpoint)
	}
}

func TestCompleteRequiresKey(t *testing.T) {
	if _, err := New(ProviderAnthropic, "", "").Complete("s", "u"); err == nil {
		t.Fatal("expected error with no API key")
	}
}

func TestCompleteSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	c := New(ProviderAnthropic, "bad", "")
	c.endpoint = srv.URL
	if _, err := c.Complete("s", "u"); err == nil {
		t.Fatal("expected API error to surface")
	}
}

func TestOpenRouterSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"message":"no credits"}}`)
	}))
	defer srv.Close()
	c := New(ProviderOpenRouter, "bad", "")
	c.endpoint = srv.URL
	if _, err := c.Complete("s", "u"); err == nil {
		t.Fatal("expected OpenRouter API error to surface")
	}
}
