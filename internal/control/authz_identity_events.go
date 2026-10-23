package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Per-identity event log for the Identities workspace. It is a small, bounded
// audit trail ("who touched this identity, when, and how") persisted in its
// own setting so the identity list itself stays the credential record.

const authzIdentityEventsSetting = "authz.identity_events"

// maxIdentityEvents caps the log per identity; the oldest entries are dropped.
const maxIdentityEvents = 50

// Event kinds.
const (
	identityEventCreated      = "created"
	identityEventUpdated      = "updated"
	identityEventChecked      = "checked"
	identityEventRefreshed    = "refreshed"
	identityEventMarkedBroken = "marked-broken"
)

// identityEvent is one entry of the per-identity log.
type identityEvent struct {
	TS     string `json:"ts"`
	Kind   string `json:"kind"`
	Actor  string `json:"actor"`
	Detail string `json:"detail,omitempty"`
}

// identityActor resolves who a write is attributed to. A caller-supplied label
// wins (the MCP tools pass the agent's own name as `owner`); an AI/MCP request
// that supplies none is recorded as "agent"; everything else — the embedded UI
// and any other human/loopback caller — is "you".
func identityActor(r *http.Request, supplied string) string {
	ai := r != nil && isAISource(r)
	if s := strings.TrimSpace(supplied); s != "" && !(ai && humanActorLabel(s)) {
		return s
	}
	if ai {
		return identitySourceAgent
	}
	return "you"
}

// humanActorLabel reports labels the UI shows as the human operator; an AI
// caller may name itself, but never as the operator.
func humanActorLabel(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "you", "ui", "human", "user", "manual", "operator":
		return true
	}
	return false
}

// identityActorSourceKind is the source.kind an identity gets when a caller
// writes its headers directly: "agent" for the AI/MCP channel, else "manual".
func identityActorSourceKind(r *http.Request) string {
	if r != nil && isAISource(r) {
		return identitySourceAgent
	}
	return identitySourceManual
}

// mutateIdentityEvents runs fn over the stored log under its own mutex. It
// must never be called while authzMu is held (mutateIdentities' lock).
func (h *authzAPI) mutateIdentityEvents(fn func(map[string][]identityEvent)) error {
	h.authzEventsMu.Lock()
	defer h.authzEventsMu.Unlock()
	cur, err := h.identityEventLog()
	if err != nil {
		return err
	}
	fn(cur)
	b, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	return h.st.SetSetting(authzIdentityEventsSetting, string(b))
}

func (h *authzAPI) identityEventLog() (map[string][]identityEvent, error) {
	raw, _, err := h.st.GetSetting(authzIdentityEventsSetting)
	if err != nil {
		return nil, err
	}
	out := map[string][]identityEvent{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// A corrupt log must never block credential work; start a fresh one.
		return map[string][]identityEvent{}, nil
	}
	return out, nil
}

// appendIdentityEvent records one event for name, keeping the newest
// maxIdentityEvents. Failures are ignored on purpose: the log is an audit
// convenience, never a precondition for the mutation that produced it.
func (h *authzAPI) appendIdentityEvent(name, kind, actor, detail string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	ev := identityEvent{TS: time.Now().UTC().Format(time.RFC3339), Kind: kind, Actor: actor, Detail: detail}
	_ = h.mutateIdentityEvents(func(log map[string][]identityEvent) {
		list := append(log[name], ev)
		if len(list) > maxIdentityEvents {
			list = list[len(list)-maxIdentityEvents:]
		}
		log[name] = list
	})
}

// identityEventsFor returns the log for one identity, oldest first.
func (h *authzAPI) identityEventsFor(name string) []identityEvent {
	log, err := h.identityEventLog()
	if err != nil {
		return nil
	}
	out := log[strings.TrimSpace(name)]
	if out == nil {
		return []identityEvent{}
	}
	return out
}

// dropIdentityEvents forgets an identity's log (identity removed).
func (h *authzAPI) dropIdentityEvents(name string) {
	_ = h.mutateIdentityEvents(func(log map[string][]identityEvent) {
		delete(log, strings.TrimSpace(name))
	})
}

// renameIdentityEvents moves an identity's log so a rename keeps its history.
func (h *authzAPI) renameIdentityEvents(from, to string) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || from == to {
		return
	}
	_ = h.mutateIdentityEvents(func(log map[string][]identityEvent) {
		list := append(log[to], log[from]...)
		if len(list) > maxIdentityEvents {
			list = list[len(list)-maxIdentityEvents:]
		}
		delete(log, from)
		log[to] = list
	})
}

// broadcastAuthzIdentities tells every connected client that the identity list
// changed. Always called outside authzMu.
func (h *authzAPI) broadcastAuthzIdentities(name string) {
	msg := map[string]any{"type": "authz.update", "what": "identities"}
	if n := strings.TrimSpace(name); n != "" {
		msg["name"] = n
	}
	h.broadcast(msg)
}

// broadcastAuthzRun tells every connected client that a run was persisted.
func (h *authzAPI) broadcastAuthzRun(runID int64) {
	h.broadcast(map[string]any{"type": "authz.update", "what": "runs", "runId": runID})
}
