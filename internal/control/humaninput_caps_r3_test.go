package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postHumanInput posts a prompt on an already-cancelled request context, so
// the handler registers the prompt and returns at once instead of waiting.
func postHumanInput(h *Hub, source, body string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/human-input", strings.NewReader(body)).WithContext(ctx)
	if source != "" {
		req.Header.Set("X-Interseptor-Source", source)
	}
	rec := httptest.NewRecorder()
	(&metaAPI{h}).createHumanInput(rec, req)
	return rec
}

// Prompts were bounded by time only: an agent could queue any number within
// the hour, each held in memory and pushed to the UI banner.
func TestHumanInputPendingPromptsAreCapped(t *testing.T) {
	h, _, _ := newHub(t)
	var refused *httptest.ResponseRecorder
	for i := 0; i < maxHumanPromptsPerSource+5; i++ {
		rec := postHumanInput(h, "ai", fmt.Sprintf(`{"message":"question %d"}`, i))
		if rec.Code == http.StatusTooManyRequests {
			refused = rec
			break
		}
	}
	if refused == nil {
		t.Fatalf("%d prompts from one source were all accepted", maxHumanPromptsPerSource+5)
	}
	if !strings.Contains(refused.Body.String(), "unanswered") {
		t.Fatalf("refusal does not tell the agent what to do: %s", refused.Body.String())
	}
	if n := len(h.hi.pending()); n != maxHumanPromptsPerSource {
		t.Fatalf("%d prompts pending, want the per-source cap %d", n, maxHumanPromptsPerSource)
	}
	// Another source still has room, up to the overall cap.
	if rec := postHumanInput(h, "", `{"message":"from another client"}`); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("one source's backlog blocked another: %s", rec.Body.String())
	}
	for i := 0; i < maxHumanPromptsTotal; i++ {
		postHumanInput(h, fmt.Sprintf("agent-%d", i), `{"message":"q"}`)
	}
	if n := len(h.hi.pending()); n != maxHumanPromptsTotal {
		t.Fatalf("%d prompts pending, want the overall cap %d", n, maxHumanPromptsTotal)
	}
	// Answering one frees a slot.
	id := h.hi.pending()[0].ID
	if !h.hi.answer(id, "ok") {
		t.Fatal("answer failed")
	}
	if rec := postHumanInput(h, "fresh", `{"message":"after an answer"}`); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("an answered prompt still counted against the cap: %s", rec.Body.String())
	}
}

func TestHumanInputBoundsMessageAndOptions(t *testing.T) {
	h, _, _ := newHub(t)
	long := strings.Repeat("a", maxHumanPromptMessageBytes+1)
	opts := `"` + strings.Repeat(`x","`, maxHumanPromptOptions) + `y"`
	for name, body := range map[string]string{
		"long message":     `{"message":"` + long + `"}`,
		"too many options": `{"message":"q","options":[` + opts + `]}`,
		"long option":      `{"message":"q","options":["` + strings.Repeat("o", maxHumanPromptOptionBytes+1) + `"]}`,
	} {
		rec := postHumanInput(h, "ai", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if n := len(h.hi.pending()); n != 0 {
		t.Fatalf("%d oversized prompts were stored", n)
	}
}
