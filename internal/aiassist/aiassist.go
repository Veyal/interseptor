// Package aiassist is an optional, bring-your-own-key bridge to an LLM that
// explains requests, suggests payloads, and summarizes findings. It is off until
// the user supplies an API key, and it only ever talks to the configured
// provider — captured traffic is sent only when the user explicitly asks.
//
// Two providers are supported: Anthropic (native Messages API) and OpenRouter
// (OpenAI-compatible chat completions, which fronts many models).
package aiassist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Providers.
const (
	ProviderAnthropic  = "anthropic"
	ProviderOpenRouter = "openrouter"
)

const (
	defaultAnthropicModel  = "claude-haiku-4-5-20251001"
	defaultOpenRouterModel = "anthropic/claude-3.5-haiku"
	anthropicEndpoint      = "https://api.anthropic.com/v1/messages"
	openRouterEndpoint     = "https://openrouter.ai/api/v1/chat/completions"
)

// Client calls a chosen LLM provider with a user-provided key.
type Client struct {
	provider string
	key      string
	model    string
	endpoint string
	cl       *http.Client
}

// New returns a client for the given provider (defaults to Anthropic for an
// unknown/empty value). An empty model uses that provider's default.
func New(provider, key, model string) *Client {
	c := &Client{provider: provider, key: key, model: model, cl: &http.Client{Timeout: 60 * time.Second}}
	switch provider {
	case ProviderOpenRouter:
		c.endpoint = openRouterEndpoint
		if c.model == "" {
			c.model = defaultOpenRouterModel
		}
	default:
		c.provider = ProviderAnthropic
		c.endpoint = anthropicEndpoint
		if c.model == "" {
			c.model = defaultAnthropicModel
		}
	}
	return c
}

// Complete sends a system + user prompt and returns the model's text reply.
func (c *Client) Complete(system, user string) (string, error) {
	if c.key == "" {
		return "", fmt.Errorf("no API key configured")
	}
	if c.provider == ProviderOpenRouter {
		return c.completeOpenRouter(system, user)
	}
	return c.completeAnthropic(system, user)
}

// ---- Anthropic (native Messages API) ----

func (c *Client) completeAnthropic(system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": 1024,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	raw, err := c.do(req)
	if err != nil {
		return "", err
	}
	var mr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &mr); err != nil {
		return "", fmt.Errorf("ai response: %s", string(raw))
	}
	if mr.Error != nil {
		return "", fmt.Errorf("ai: %s", mr.Error.Message)
	}
	var out string
	for _, p := range mr.Content {
		if p.Type == "text" {
			out += p.Text
		}
	}
	return out, nil
}

// ---- OpenRouter (OpenAI-compatible chat completions) ----

func (c *Client) completeOpenRouter(system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	// OpenRouter attribution headers (optional but recommended).
	req.Header.Set("HTTP-Referer", "https://github.com/Veyal/interceptor")
	req.Header.Set("X-Title", "Interceptor")

	raw, err := c.do(req)
	if err != nil {
		return "", err
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("ai response: %s", string(raw))
	}
	if cr.Error != nil {
		return "", fmt.Errorf("ai: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("ai: empty response")
	}
	return cr.Choices[0].Message.Content, nil
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
