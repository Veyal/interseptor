package control

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Human-input handoff: the AI calls request_human_input to pause and ask the
// operator a question (e.g. "found IDOR — fuzz ids 1-100?") before a high-impact
// or ambiguous action. The control endpoint blocks the AI's call for a short
// window (under the MCP client's 60s timeout) so a quick answer returns inline;
// otherwise the AI polls get_human_response. Prompts live in memory (a blocked
// call holds the state); the UI lists pending ones so an SSE reconnect recovers.

const humanInputWait = 40 * time.Second

type humanPrompt struct {
	ID       int64    `json:"id"`
	TS       int64    `json:"ts"`
	Message  string   `json:"message"`
	Options  []string `json:"options,omitempty"`
	Answered bool     `json:"answered"`
	Answer   string   `json:"answer,omitempty"`
	done     chan struct{}
}

type humanInput struct {
	mu      sync.Mutex
	seq     int64
	prompts map[int64]*humanPrompt
}

func newHumanInput() *humanInput { return &humanInput{prompts: map[int64]*humanPrompt{}} }

func (hi *humanInput) create(msg string, opts []string) *humanPrompt {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	hi.seq++
	p := &humanPrompt{ID: hi.seq, TS: time.Now().UnixMilli(), Message: msg, Options: opts, done: make(chan struct{})}
	hi.prompts[p.ID] = p
	return p
}

func (hi *humanInput) get(id int64) *humanPrompt {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	return hi.prompts[id]
}

func (hi *humanInput) pending() []humanPrompt {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	out := []humanPrompt{}
	for _, p := range hi.prompts {
		if !p.Answered {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// answer resolves a pending prompt and unblocks the waiting AI call. Returns false
// if the id is unknown or already answered.
func (hi *humanInput) answer(id int64, ans string) bool {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	p := hi.prompts[id]
	if p == nil || p.Answered {
		return false
	}
	p.Answer, p.Answered = ans, true
	close(p.done)
	return true
}

// createHumanInput (POST /api/human-input) registers a prompt and blocks up to
// humanInputWait for the human to answer; then returns the (possibly still
// pending) prompt so the AI can either use the answer or poll for it.
func (h *Hub) createHumanInput(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string   `json:"message"`
		Options []string `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Message == "" {
		httpErr(w, http.StatusBadRequest, "message required")
		return
	}
	p := h.hi.create(in.Message, in.Options)
	h.broadcast(map[string]any{"type": "human.input"})
	select {
	case <-p.done:
	case <-time.After(humanInputWait):
	case <-r.Context().Done():
		return
	}
	writeJSON(w, http.StatusOK, h.hi.get(p.ID))
}

// listHumanInput (GET /api/human-input) returns the pending prompts (UI load /
// SSE-reconnect recovery).
func (h *Hub) listHumanInput(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prompts": h.hi.pending()})
}

// getHumanInput (GET /api/human-input/{id}) — the AI polls for an answer.
func (h *Hub) getHumanInput(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p := h.hi.get(id)
	if p == nil {
		httpErr(w, http.StatusNotFound, "no such prompt")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// respondHumanInput (POST /api/human-input/{id}/respond) — the human answers.
func (h *Hub) respondHumanInput(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !h.hi.answer(id, in.Answer) {
		httpErr(w, http.StatusNotFound, "no such pending prompt")
		return
	}
	h.broadcast(map[string]any{"type": "human.input"})
	w.WriteHeader(http.StatusNoContent)
}
