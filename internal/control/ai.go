package control

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/scanner"
)

// aiAssist asks a bring-your-own-key LLM to explain a flow, suggest payloads, or
// summarize findings. Disabled unless an API key is configured (Settings or the
// provider's env var). The exchange is sent to the provider only here, on an
// explicit request. Provider is "anthropic" (default) or "openrouter".
func (h *Hub) aiAssist(w http.ResponseWriter, r *http.Request) {
	provider, _, _ := h.st.GetSetting("ai.provider")
	if provider == "" {
		provider = aiassist.ProviderAnthropic
	}
	key, _, _ := h.st.GetSetting("ai.apiKey")
	if key == "" {
		if provider == aiassist.ProviderOpenRouter {
			key = os.Getenv("OPENROUTER_API_KEY")
		} else {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	if key == "" {
		httpErr(w, http.StatusBadRequest, "no AI API key — set one in Settings → AI assist (or the ANTHROPIC_API_KEY / OPENROUTER_API_KEY env var)")
		return
	}
	var in struct {
		FlowID int64  `json:"flowId"`
		Kind   string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}

	reqRaw := clip(string(h.rawRequest(f)), 4000)
	resRaw := clip(string(h.rawResponse(f)), 4000)
	const system = "You are a concise web application security testing assistant helping a penetration tester. Be specific and practical. Do not include disclaimers."

	var user string
	switch in.Kind {
	case "suggest":
		user = "Suggest specific test payloads (injection, IDOR, auth bypass, etc.) for the parameters in this request, with a one-line rationale each:\n\n" + reqRaw
	case "summarize":
		issues := scanner.Analyze(f, h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash))
		var findings string
		for _, is := range issues {
			findings += "- " + is.Severity + ": " + is.Title + "\n"
		}
		user = "Summarize the security posture of this exchange in a few bullets. Passive findings:\n" + findings + "\nRequest:\n" + reqRaw + "\n\nResponse:\n" + resRaw
	default:
		user = "Explain what this HTTP request/response does and anything security-relevant a tester should check:\n\nRequest:\n" + reqRaw + "\n\nResponse:\n" + resRaw
	}

	model, _, _ := h.st.GetSetting("ai.model")
	text, err := aiassist.New(provider, key, model).Complete(system, user)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n…(truncated)"
	}
	return s
}
