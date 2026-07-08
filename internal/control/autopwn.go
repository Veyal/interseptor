package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/aiassist"
	"github.com/Veyal/interseptor/internal/autopwn"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
)

// autopwnAskTimeout bounds how long an autonomous run blocks on a human confirm
// (Gate 4) before proceeding without an answer. It is deliberately far longer
// than the MCP-facing humanInputWait (40s): an autonomous run is a background
// job, so an operator may take minutes to answer a Critical/High confirm.
const autopwnAskTimeout = 30 * time.Minute

// autopwn returns the autonomous-pentest engine, building it (and its in-process
// tool bus) lazily on first use. Construction is deferred out of New() because
// the engine's Tools bus and OOBBaseURL both need this control server's own
// loopback address, which cmd only binds AFTER New() returns. By the time any
// handler asks for the engine the listener is up, so currentControlAddr() is
// meaningful. The engine is a singleton (at most one run at a time), guarded by
// autopwnMu.
func (h *Hub) autopwn() *autopwn.Engine {
	h.autopwnMu.Lock()
	defer h.autopwnMu.Unlock()
	if h.autopwnEngine == nil {
		// The tool bus points at this control server's own loopback address so tool
		// calls loop back through the /api surface — loopback-guard-allowed without an
		// API key (only /mcp requires a bearer token, and Server.Call never touches
		// /mcp; it does REST to /api/...). So no key is minted or carried.
		h.autopwnTools = mcp.New("http://" + h.currentControlAddr())
		h.autopwnEngine = autopwn.New(autopwn.Deps{
			Store:  h.st,
			Sender: h.snd,
			OOB:    h.oob,
			Tools:  h.autopwnTools,

			// NewToolCaller resolves the CURRENTLY configured provider/key/model/endpoint
			// on every call (so a Settings change takes effect on the next phase) and
			// wraps an aiassist client as a provider-agnostic ToolCaller.
			NewToolCaller: h.autopwnToolCaller,

			Broadcast:      h.broadcast,
			RecordActivity: h.autopwnRecordActivity,
			AskHuman:       h.autopwnAskHuman,

			OOBBaseURL:    h.autopwnOOBBase(),
			IsOwnListener: h.autopwnIsOwnListener,

			Clock: aiagent.RealClock{},
		})
	}
	return h.autopwnEngine
}

// autopwnToolCaller builds a fresh ToolCaller from the current AI settings. The
// engine calls this once per agent phase, so provider/model changes apply live.
func (h *Hub) autopwnToolCaller() (aiagent.ToolCaller, error) {
	provider, key, endpoint, ok := h.aiCreds()
	if !ok {
		return nil, errors.New(aiNoKeyMsg)
	}
	model, _, _ := h.st.GetSetting("ai.model")
	return aiagent.NewClientToolCaller(aiassist.New(provider, key, model, endpoint))
}

// autopwnRecordActivity writes an engine step straight to the persisted Activity
// feed and broadcasts it live — mirroring recordActivity + the activity SSE frame
// (NOT the POST /api/activity loopback, which would re-tag it and add a hop).
func (h *Hub) autopwnRecordActivity(a store.Activity) {
	it := (&metaAPI{h}).recordActivity(a)
	h.broadcast(map[string]any{"type": "activity", "item": it})
}

// autopwnAskHuman wraps the humaninput gate for the engine's Gate-4 confirm: it
// registers a prompt (notifying the UI), blocks up to autopwnAskTimeout for the
// operator's answer, and returns the chosen option. On timeout/cancel it returns
// an empty answer with a nil error so the engine can apply its own default.
func (h *Hub) autopwnAskHuman(ctx context.Context, message string, options []string) (string, error) {
	p := h.hi.create(message, options)
	h.broadcast(map[string]any{"type": "human.input"})
	select {
	case <-p.done:
		return h.hi.get(p.ID).Answer, nil
	case <-time.After(autopwnAskTimeout):
		return "", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// autopwnOOBBase resolves the OOB callback base the engine injects into blind
// probes. It prefers the operator-set public base (target-reachable), falling
// back to this control server's own loopback origin (useful only for local
// self-testing). Load-bearing for blind vuln classes, so it always returns a
// best-available base rather than empty.
func (h *Hub) autopwnOOBBase() string {
	base, _, _ := h.st.GetSetting("oob.baseUrl")
	if base == "" {
		base = "http://" + h.currentControlAddr() + "/oob"
	}
	return base
}

// autopwnIsOwnListener parses a raw target URL into a host/port and defers to the
// existing isOwnListener predicate, so the verify phase never probes one of
// Interseptor's own loopback listeners (control plane / proxy).
func (h *Hub) autopwnIsOwnListener(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return h.isOwnListener(&store.Flow{Host: u.Hostname(), Port: atoiOr(u.Port(), defaultPortFor(u.Scheme))})
}

// ---- REST handlers ----

// autopwnStartReq is the JSON body of POST /api/autopwn/start.
type autopwnStartReq struct {
	Budget struct {
		MaxRequests int   `json:"maxRequests"`
		MaxTokens   int   `json:"maxTokens"`
		MaxWallMs   int64 `json:"maxWallMs"`
	} `json:"budget"`
	TargetHint string `json:"targetHint"`
}

// autopwnStart launches an autonomous run. The run must OUTLIVE this request, so
// it starts under context.Background() (with the engine's own kill switch) rather
// than r.Context(), which is cancelled the moment this handler returns. The reply
// is immediate: {runId} plus the freshly-planning state.
func (h *Hub) autopwnStart(w http.ResponseWriter, r *http.Request) {
	var in autopwnStartReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && err != io.EOF {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	opts := autopwn.StartOpts{
		Budget: autopwn.Budget{
			MaxRequests: in.Budget.MaxRequests,
			MaxTokens:   in.Budget.MaxTokens,
			MaxWallMs:   in.Budget.MaxWallMs,
		},
		TargetHint: in.TargetHint,
	}
	// Start under a background context, NOT r.Context(): the run must outlive this
	// request. The handler returns the runId immediately while the run goroutine
	// keeps executing under the engine's own kill-switch ctx (Stop cancels it).
	runID, err := h.autopwn().Start(context.Background(), opts)
	if err != nil {
		switch {
		case errors.Is(err, autopwn.ErrNoScope):
			httpErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, autopwn.ErrRunActive):
			httpErr(w, http.StatusConflict, err.Error())
		default:
			httpErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runId": runID, "state": h.autopwn().State()})
}

// autopwnStop cancels the active run (the kill switch) and returns the state.
func (h *Hub) autopwnStop(w http.ResponseWriter, r *http.Request) {
	h.autopwn().Stop()
	writeJSON(w, http.StatusOK, h.autopwn().State())
}

// autopwnStateHandler returns a live snapshot of the current/last run.
func (h *Hub) autopwnStateHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.autopwn().State())
}

// autopwnRuns returns the persisted run history, newest first.
func (h *Hub) autopwnRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.autopwn().ListRuns()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []store.PentestRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}
