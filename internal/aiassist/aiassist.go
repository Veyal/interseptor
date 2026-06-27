// Package aiassist is an optional, bring-your-own-key bridge to an LLM that
// explains requests, suggests payloads, and summarizes findings. It is off until
// the user supplies an API key, and it only ever talks to the configured
// provider — captured traffic is sent only when the user explicitly asks.
//
// Two providers are supported: Anthropic (native Messages API) and OpenRouter
// (OpenAI-compatible chat completions, which fronts many models).
package aiassist

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	// maxTokens caps the reply length. Kept modest so answers stay fast and tight —
	// the assistant is told to be brief, and this is the hard ceiling.
	maxTokens = 768
)

// Message is one turn in a multi-message completion (user or assistant).
type Message struct {
	Role    string
	Content string
}

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
	return c.CompleteMessages(system, []Message{{Role: "user", Content: user}})
}

// CompleteMessages sends a system prompt and an ordered message history.
func (c *Client) CompleteMessages(system string, messages []Message) (string, error) {
	if c.key == "" {
		return "", fmt.Errorf("no API key configured")
	}
	if c.provider == ProviderOpenRouter {
		return c.completeOpenRouterMessages(system, messages)
	}
	return c.completeAnthropicMessages(system, messages)
}

// ---- Anthropic (native Messages API) ----

func (c *Client) completeAnthropicMessages(system string, messages []Message) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   messageMaps(messages),
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

func (c *Client) completeOpenRouterMessages(system string, messages []Message) (string, error) {
	msgs := []map[string]string{{"role": "system", "content": system}}
	msgs = append(msgs, messageMaps(messages)...)
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"messages":   msgs,
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

// ---- streaming ----

// streamHTTP has no overall timeout — a streamed completion can legitimately run
// for tens of seconds; cancellation is driven by the caller's context instead.
var streamHTTP = &http.Client{}

// CompleteStream sends the prompt and invokes onDelta for each incremental text
// chunk as the model generates it (Server-Sent Events). It returns when the
// stream ends, the provider reports an error, or ctx is cancelled. onDelta is
// always called from the calling goroutine.
func (c *Client) CompleteStream(ctx context.Context, system, user string, onDelta func(string)) error {
	return c.CompleteStreamMessages(ctx, system, []Message{{Role: "user", Content: user}}, onDelta)
}

// CompleteStreamMessages is the multi-turn streaming variant of CompleteMessages.
func (c *Client) CompleteStreamMessages(ctx context.Context, system string, messages []Message, onDelta func(string)) error {
	if c.key == "" {
		return fmt.Errorf("no API key configured")
	}
	if c.provider == ProviderOpenRouter {
		return c.streamOpenRouterMessages(ctx, system, messages, onDelta)
	}
	return c.streamAnthropicMessages(ctx, system, messages, onDelta)
}

func (c *Client) streamAnthropicMessages(ctx context.Context, system string, messages []Message, onDelta func(string)) error {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"stream":     true,
		"system":     system,
		"messages":   messageMaps(messages),
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

func (c *Client) streamOpenRouterMessages(ctx context.Context, system string, messages []Message, onDelta func(string)) error {
	msgs := []map[string]string{{"role": "system", "content": system}}
	msgs = append(msgs, messageMaps(messages)...)
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   msgs,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/Veyal/interceptor")
	req.Header.Set("X-Title", "Interceptor")
	return c.stream(req, onDelta, openRouterDelta)
}

// stream runs an SSE request and feeds each data line through deltaOf, forwarding
// any non-empty text to onDelta.
func (c *Client) stream(req *http.Request, onDelta func(string), deltaOf func(string) (string, error)) error {
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if msg := apiErrorMessage(raw); msg != "" {
			return fmt.Errorf("ai: %s", msg)
		}
		return fmt.Errorf("ai: HTTP %d", resp.StatusCode)
	}
	return parseSSE(resp.Body, func(data string) error {
		text, err := deltaOf(data)
		if err != nil {
			return err
		}
		if text != "" {
			onDelta(text)
		}
		return nil
	})
}

// parseSSE reads a text/event-stream body line by line, passing each `data:`
// payload to onData. Blank lines, comments, and the OpenAI `[DONE]` sentinel are
// skipped. Returns the first onData error (used to surface a mid-stream provider
// error) or any read error.
func parseSSE(r io.Reader, onData func(string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // some deltas (e.g. base64) can be large
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" || data == "[DONE]" {
			continue
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	return sc.Err()
}

// anthropicDelta extracts the text from one Anthropic stream event. Non-text
// events (message_start, ping, …) yield "". A streamed error event surfaces as
// an error so the caller can abort.
func anthropicDelta(data string) (string, error) {
	var ev struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return "", nil // tolerate keepalive/comment frames
	}
	if ev.Error != nil {
		return "", fmt.Errorf("ai: %s", ev.Error.Message)
	}
	if ev.Delta.Type == "text_delta" {
		return ev.Delta.Text, nil
	}
	return "", nil
}

// openRouterDelta extracts the content from one OpenAI-style chat stream chunk.
func openRouterDelta(data string) (string, error) {
	var ev struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return "", nil
	}
	if ev.Error != nil {
		return "", fmt.Errorf("ai: %s", ev.Error.Message)
	}
	if len(ev.Choices) > 0 {
		return ev.Choices[0].Delta.Content, nil
	}
	return "", nil
}

// apiErrorMessage pulls a provider error message out of a non-streamed error body
// (both providers wrap it as {"error":{"message":...}}).
func messageMaps(messages []Message) []map[string]string {
	out := make([]map[string]string, len(messages))
	for i, m := range messages {
		out[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	return out
}

func apiErrorMessage(raw []byte) string {
	var e struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil {
		return e.Error.Message
	}
	return strings.TrimSpace(string(raw))
}
