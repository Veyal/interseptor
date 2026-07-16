package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// Saved AI provider profiles. Users keep several {provider, key, model, endpoint}
// configurations and switch the active one with a click. Activating a profile
// mirrors its fields into the canonical ai.provider/ai.apiKey/ai.model/ai.endpoint
// settings — the single source everything else already reads (aiCreds, autopwn,
// checks/notes AI) — so nothing downstream needs to know about profiles.

// aiProfile is one saved provider configuration. The API key is stored but never
// returned to the client (hasKey is surfaced instead).
type aiProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
}

func (h *Hub) loadAiProfiles() []aiProfile {
	raw, _, _ := h.st.GetSetting("ai.profiles")
	if raw == "" {
		return nil
	}
	var ps []aiProfile
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return nil
	}
	return ps
}

func (h *Hub) saveAiProfiles(ps []aiProfile) error {
	b, _ := json.Marshal(ps)
	return h.st.SetSetting("ai.profiles", string(b))
}

func newProfileID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "p_" + hex.EncodeToString(b)
}

// listAiProviders returns the saved profiles (keys redacted) plus the active id.
func (h *Hub) listAiProviders(w http.ResponseWriter, r *http.Request) {
	ps := h.loadAiProfiles()
	active, _, _ := h.st.GetSetting("ai.activeProfile")
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "provider": p.Provider,
			"model": p.Model, "endpoint": p.Endpoint, "hasKey": p.APIKey != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "activeId": active})
}

// saveAiProvider upserts a profile. On update with an empty apiKey the stored key
// is preserved (so the user can rename/retune without re-entering the secret).
func (h *Hub) saveAiProvider(w http.ResponseWriter, r *http.Request) {
	if h.aiDisabled() {
		httpErr(w, http.StatusForbidden, aiDisabledMsg)
		return
	}
	var in aiProfile
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.TrimSpace(in.Provider)
	if in.Name == "" {
		in.Name = in.Provider
	}
	if in.Provider == "" {
		httpErr(w, http.StatusBadRequest, "provider is required")
		return
	}
	ps := h.loadAiProfiles()
	if in.ID != "" {
		found := false
		for i := range ps {
			if ps[i].ID == in.ID {
				if in.APIKey == "" {
					in.APIKey = ps[i].APIKey // keep existing secret
				}
				ps[i] = in
				found = true
				break
			}
		}
		if !found {
			ps = append(ps, in)
		}
	} else {
		// New profile. If no key was supplied (e.g. "save the current config as a
		// profile" with the masked key field left blank), snapshot the live active
		// key so the saved profile is actually usable.
		if in.APIKey == "" {
			in.APIKey, _, _ = h.st.GetSetting("ai.apiKey")
		}
		in.ID = newProfileID()
		ps = append(ps, in)
	}
	if err := h.saveAiProfiles(ps); err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID})
}

// deleteAiProvider removes a profile by id.
func (h *Hub) deleteAiProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps := h.loadAiProfiles()
	out := ps[:0]
	for _, p := range ps {
		if p.ID != id {
			out = append(out, p)
		}
	}
	if err := h.saveAiProfiles(out); err != nil {
		httpInternalErr(w, err)
		return
	}
	if active, _, _ := h.st.GetSetting("ai.activeProfile"); active == id {
		_ = h.st.SetSetting("ai.activeProfile", "")
	}
	w.WriteHeader(http.StatusNoContent)
}

// activateAiProvider makes a saved profile the active one: it copies the profile's
// fields into the canonical ai.* settings and records the active id, then broadcasts
// a settings.update so the UI (and any live consumer) picks up the switch.
func (h *Hub) activateAiProvider(w http.ResponseWriter, r *http.Request) {
	if h.aiDisabled() {
		httpErr(w, http.StatusForbidden, aiDisabledMsg)
		return
	}
	id := r.PathValue("id")
	var chosen *aiProfile
	for _, p := range h.loadAiProfiles() {
		if p.ID == id {
			pp := p
			chosen = &pp
			break
		}
	}
	if chosen == nil {
		httpErr(w, http.StatusNotFound, "no such provider profile")
		return
	}
	for k, v := range map[string]string{
		"ai.provider": chosen.Provider,
		"ai.apiKey":   chosen.APIKey,
		"ai.model":    chosen.Model,
		"ai.endpoint": chosen.Endpoint,
	} {
		if err := h.st.SetSetting(k, v); err != nil {
			httpInternalErr(w, err)
			return
		}
	}
	_ = h.st.SetSetting("ai.activeProfile", id)
	h.broadcast(map[string]any{"type": "settings.update"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": chosen.Provider, "model": chosen.Model})
}
