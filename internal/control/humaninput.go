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

// humanInputExpiry bounds how long an unanswered prompt lives. Without this,
// an abandoned AI question (operator never responds) stays in the prompts
// map — and the UI banner — forever, slowly accumulating memory and clutter
// across a long-running session. An hour is long enough that a genuinely
// slow-to-respond operator isn't cut off, but short enough that stale
// prompts don't pile up indefinitely.
const humanInputExpiry = time.Hour

const expiredAnswerMsg = "expired — no answer given"

type humanPrompt struct {
	ID       int64    `json:"id"`
	TS       int64    `json:"ts"`
	Message  string   `json:"message"`
	Options  []string `json:"options,omitempty"`
	Answered bool     `json:"answered"`
	Expired  bool     `json:"expired,omitempty"`
	Answer   string   `json:"answer,omitempty"`
	done     chan struct{}
}

type humanInput struct {
	mu      sync.Mutex
	seq     int64
	prompts map[int64]*humanPrompt
	now     func() time.Time // overridden in tests
}

func newHumanInput() *humanInput {
	return &humanInput{prompts: map[int64]*humanPrompt{}, now: time.Now}
}

func (hi *humanInput) create(msg string, opts []string) *humanPrompt {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	hi.seq++
	p := &humanPrompt{ID: hi.seq, TS: hi.now().UnixMilli(), Message: msg, Options: opts, done: make(chan struct{})}
	hi.prompts[p.ID] = p
	return p
}

// get returns the prompt, lazily expiring it first if it's been pending
// longer than humanInputExpiry.
func (hi *humanInput) get(id int64) *humanPrompt {
	hi.mu.Lock()
	p, expired := hi.expireLocked(id)
	hi.mu.Unlock()
	if expired {
		hi.scheduleCleanup(id)
	}
	return p
}

func (hi *humanInput) pending() []humanPrompt {
	hi.mu.Lock()
	ids := make([]int64, 0, len(hi.prompts))
	for id := range hi.prompts {
		ids = append(ids, id)
	}
	var expiredIDs []int64
	out := []humanPrompt{}
	for _, id := range ids {
		p, expired := hi.expireLocked(id)
		if expired {
			expiredIDs = append(expiredIDs, id)
		}
		if p != nil && !p.Answered {
			out = append(out, *p)
		}
	}
	hi.mu.Unlock()
	for _, id := range expiredIDs {
		hi.scheduleCleanup(id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// expireLocked checks prompt id and, if it's still pending past
// humanInputExpiry, marks it answered+expired and unblocks any waiter. Must
// be called with hi.mu held. Returns the (possibly now-expired) prompt and
// whether it was just expired by this call (so the caller can schedule
// cleanup outside the lock).
func (hi *humanInput) expireLocked(id int64) (*humanPrompt, bool) {
	p := hi.prompts[id]
	if p == nil || p.Answered {
		return p, false
	}
	if hi.now().Sub(time.UnixMilli(p.TS)) < humanInputExpiry {
		return p, false
	}
	p.Answer, p.Answered, p.Expired = expiredAnswerMsg, true, true
	close(p.done)
	return p, true
}

// answer resolves a pending prompt and unblocks the waiting AI call. Returns false
// if the id is unknown, already answered, or already expired.
func (hi *humanInput) answer(id int64, ans string) bool {
	hi.mu.Lock()
	p := hi.prompts[id]
	if p == nil || p.Answered {
		hi.mu.Unlock()
		return false
	}
	p.Answer, p.Answered = ans, true
	close(p.done)
	hi.mu.Unlock()
	hi.scheduleCleanup(id)
	return true
}

// scheduleCleanup removes a resolved (answered or expired) prompt from the
// map after a short delay, giving the UI/poller time to observe the final
// state before it disappears.
func (hi *humanInput) scheduleCleanup(id int64) {
	go func(pid int64) {
		time.Sleep(time.Minute)
		hi.mu.Lock()
		delete(hi.prompts, pid)
		hi.mu.Unlock()
	}(id)
}

// createHumanInput (POST /api/human-input) registers a prompt and blocks up to
// humanInputWait for the human to answer; then returns the (possibly still
// pending) prompt so the AI can either use the answer or poll for it.
func (h *metaAPI) createHumanInput(w http.ResponseWriter, r *http.Request) {
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
func (h *metaAPI) listHumanInput(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prompts": h.hi.pending()})
}

// getHumanInput (GET /api/human-input/{id}) — the AI polls for an answer.
func (h *metaAPI) getHumanInput(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p := h.hi.get(id)
	if p == nil {
		httpErr(w, http.StatusNotFound, "no such prompt")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// respondHumanInput (POST /api/human-input/{id}/respond) — the human answers.
func (h *metaAPI) respondHumanInput(w http.ResponseWriter, r *http.Request) {
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
