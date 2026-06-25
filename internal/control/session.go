package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Veyal/interceptor/internal/sender"
)

// Session/auth handling (scoped slice): a set of headers — typically an
// Authorization bearer token or a Cookie — auto-applied to every Repeater and
// Intruder send so the tester (or the AI) need not re-paste a token on each
// request. Stored in settings; applied to the shared sender. The fuller bets
// (login-macro recording, automatic re-auth on 401) remain on the roadmap.

// parseSessionHeaders turns "Key: Value" lines into sender.Header entries.
// Blank lines and lines beginning with '#' are ignored.
func parseSessionHeaders(text string) []sender.Header {
	var out []sender.Header
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out = append(out, sender.Header{Key: k, Value: strings.TrimSpace(v)})
	}
	return out
}

// loadMacro reads the persisted token-refresh macro ("" setting → disabled).
func (h *Hub) loadMacro() sender.Macro {
	raw, _, _ := h.st.GetSetting("session.macro")
	var m sender.Macro
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// applySessionFromStore loads persisted session config + macro and applies both.
func (h *Hub) applySessionFromStore() {
	enabled, _, _ := h.st.GetSetting("session.enabled")
	text, _, _ := h.st.GetSetting("session.headers")
	h.snd.SetSession(enabled == "1", parseSessionHeaders(text))
	h.snd.SetMacro(h.loadMacro())
}

func (h *Hub) getSession(w http.ResponseWriter, r *http.Request) {
	enabled, _, _ := h.st.GetSetting("session.enabled")
	text, _, _ := h.st.GetSetting("session.headers")
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled == "1", "headers": text, "macro": h.loadMacro()})
}

func (h *Hub) setSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool          `json:"enabled"`
		Headers string        `json:"headers"`
		Macro   *sender.Macro `json:"macro"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	en := ""
	if in.Enabled {
		en = "1"
	}
	_ = h.st.SetSetting("session.enabled", en)
	_ = h.st.SetSetting("session.headers", in.Headers)
	h.snd.SetSession(in.Enabled, parseSessionHeaders(in.Headers))
	if in.Macro != nil {
		b, _ := json.Marshal(*in.Macro)
		_ = h.st.SetSetting("session.macro", string(b))
		h.snd.SetMacro(*in.Macro)
	}
	h.broadcast(map[string]any{"type": "session.update"})
	writeJSON(w, http.StatusOK, map[string]any{"enabled": in.Enabled, "headers": in.Headers, "macro": h.loadMacro()})
}
