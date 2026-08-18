package control

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Project-scoped UI blobs (Repeater/Intruder tabs + Intruder presets). Stored in
// the project SQLite settings table so export/switch/restore keep engagement
// drafts with the project — not just browser localStorage (#17/#18 follow-up).
const (
	uiRepeaterTabsKey    = "ui.repeaterTabs"
	uiIntruderTabsKey    = "ui.intruderTabs"
	uiIntruderPresetsKey = "ui.intruderPresets"
	maxUIStateBytes      = 4 << 20
)

func uiStateKey(panel string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(panel)) {
	case "repeater", "rep.tabs", "repeater-tabs":
		return uiRepeaterTabsKey, true
	case "intruder", "intr.tabs", "intruder-tabs":
		return uiIntruderTabsKey, true
	case "intruder-presets", "intruder.presets", "presets":
		return uiIntruderPresetsKey, true
	default:
		return "", false
	}
}

func (h *Hub) getUIState(w http.ResponseWriter, r *http.Request) {
	key, ok := uiStateKey(r.PathValue("panel"))
	if !ok {
		httpErr(w, http.StatusBadRequest, "unknown panel (repeater|intruder|intruder-presets)")
		return
	}
	v, found, err := h.st.GetSetting(key)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !found || strings.TrimSpace(v) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"panel": r.PathValue("panel"), "value": nil})
		return
	}
	var raw any
	if err := json.Unmarshal([]byte(v), &raw); err != nil {
		// Corrupt blob — surface empty rather than 500 so the UI can reseeds.
		writeJSON(w, http.StatusOK, map[string]any{"panel": r.PathValue("panel"), "value": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"panel": r.PathValue("panel"), "value": raw})
}

func (h *Hub) putUIState(w http.ResponseWriter, r *http.Request) {
	key, ok := uiStateKey(r.PathValue("panel"))
	if !ok {
		httpErr(w, http.StatusBadRequest, "unknown panel (repeater|intruder|intruder-presets)")
		return
	}
	body, ok := readLimitedBody(w, r, maxUIStateBytes)
	if !ok {
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		httpErr(w, http.StatusBadRequest, "empty body")
		return
	}
	if !json.Valid(body) {
		httpErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if err := h.st.SetSetting(key, string(body)); err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "panel": r.PathValue("panel")})
}
