package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/scanner"
)

// assistSystem is the system prompt for the explain/suggest/summarize prose modes.
// Brevity is explicit: shorter replies finish faster and read better in the modal.
const assistSystem = "Concise web-app security testing assistant for a pentester. Answer in at most ~150 words or 6 short Markdown bullets, lead with the most security-relevant point, and skip all preamble, disclaimers, and restating the request."

// aiNoKeyMsg is the user-facing error when no provider key is configured.
const aiNoKeyMsg = "no AI API key — set one in Settings → AI assist (or the ANTHROPIC_API_KEY / OPENROUTER_API_KEY env var)"

// aiCreds resolves the provider and key from Settings, falling back to the
// provider's env var. ok is false when no key is available (assist is disabled).
func (h *Hub) aiCreds() (provider, key string, ok bool) {
	if h.aiDisabled() {
		return "", "", false
	}
	provider, _, _ = h.st.GetSetting("ai.provider")
	if provider == "" {
		provider = aiassist.ProviderAnthropic
	}
	key, _, _ = h.st.GetSetting("ai.apiKey")
	if key == "" {
		if provider == aiassist.ProviderOpenRouter {
			key = os.Getenv("OPENROUTER_API_KEY")
		} else {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	return provider, key, key != ""
}

// aiAssistReq is the JSON body shared by the assist endpoints: a single flow
// (back-compat) or a selection, plus the kind (explain/suggest/summarize).
type aiAssistReq struct {
	FlowID   int64          `json:"flowId"`
	FlowIDs  []int64        `json:"flowIds"`
	Kind     string         `json:"kind"`
	Question string         `json:"question"` // free-text question (kind == "ask")
	History  []aiAssistTurn `json:"history"`  // prior user/assistant turns (kind == "ask" follow-ups)
	Agent    bool           `json:"agent"`    // opt-in: let the model send requests (kind == "ask")
}

// aiAssistTurn is one message in an Ask AI follow-up thread.
type aiAssistTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (in aiAssistReq) ids() []int64 {
	if len(in.FlowIDs) > 0 {
		return in.FlowIDs
	}
	if in.FlowID != 0 {
		return []int64{in.FlowID}
	}
	return nil
}

// collectAssistFlows loads up to 20 flows as prompt-ready text (request, response,
// and — for "summarize" — the passive findings). Per-flow byte budget shrinks for
// a multi-flow selection to keep the combined prompt manageable.
func (h *aiAPI) collectAssistFlows(ids []int64, kind string) []assistFlow {
	const maxFlows = 20
	if len(ids) > maxFlows {
		ids = ids[:maxFlows]
	}
	per := 2500 // single-flow budget — smaller prompt = faster first token
	if len(ids) > 1 {
		per = 1500
	}
	var flows []assistFlow
	for _, id := range ids {
		f, err := h.st.GetFlow(id)
		if err != nil {
			continue
		}
		af := assistFlow{
			Label: fmt.Sprintf("#%d %s %s://%s%s", f.ID, f.Method, f.Scheme, f.Host, f.Path),
			Req:   clip(string(h.rawRequest(f)), per),
			Res:   clip(string(h.rawResponse(f)), per),
		}
		if kind == "summarize" {
			for _, is := range scanner.AnalyzeWithDisabled(f, h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash), h.checksDisabledSet()) {
				af.Findings += "- " + is.Severity + ": " + is.Title + "\n"
			}
		}
		flows = append(flows, af)
	}
	return flows
}

// aiAssist asks a bring-your-own-key LLM to explain a flow, suggest payloads, or
// summarize findings (non-streaming; used as the fallback when the browser can't
// consume the SSE stream). Disabled unless an API key is configured. The exchange
// is sent to the provider only here, on an explicit request.
func (h *aiAPI) aiAssist(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in aiAssistReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	ids := in.ids()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no flow selected")
		return
	}
	flows := h.collectAssistFlows(ids, in.Kind)
	if len(flows) == 0 {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	model, _, _ := h.st.GetSetting("ai.model")
	msgs := assistMessages(in.Kind, flows, in.Question, in.History)
	text, err := aiassist.New(provider, key, model).CompleteMessages(assistSystem, msgs)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}

// aiAssistStream is the streaming variant of aiAssist: it relays the model's reply
// to the browser token-by-token as Server-Sent Events (`data:` text chunks, then a
// terminal `event: done` or `event: error`). This is the primary path — it makes
// the assistant feel responsive instead of stalling on a full completion.
func (h *aiAPI) aiAssistStream(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in aiAssistReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	ids := in.ids()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no flow selected")
		return
	}
	flows := h.collectAssistFlows(ids, in.Kind)
	if len(flows) == 0 {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering so deltas arrive live
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	model, _, _ := h.st.GetSetting("ai.model")
	if in.Agent && in.Kind == "ask" {
		h.aiAssistAgentStream(w, r, in, flows, provider, key, model, flusher)
		return
	}

	msgs := assistMessages(in.Kind, flows, in.Question, in.History)
	err := aiassist.New(provider, key, model).CompleteStreamMessages(r.Context(), assistSystem, msgs, func(delta string) {
		b, _ := json.Marshal(delta) // JSON-encode so newlines/quotes survive the SSE framing
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	})
	if err != nil {
		b, _ := json.Marshal(err.Error())
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
	} else {
		fmt.Fprint(w, "event: done\ndata: {}\n\n")
	}
	flusher.Flush()
}

// aiPayload is one actionable test suggestion: where to inject, the exact string,
// a one-line rationale, and which tool fits — "repeater" for a one-shot manual
// probe, "intruder" for fuzzing/enumeration over many values.
type aiPayload struct {
	Point   string `json:"point"`
	Payload string `json:"payload"`
	Why     string `json:"why"`
	Tool    string `json:"tool"`
}

// actionsSystem forces a bare-JSON reply (no prose, no fences) for the structured
// payload suggestions that the UI turns into one-click Intruder actions.
const actionsSystem = "You are a web-app security testing assistant. Reply with ONLY a JSON array and nothing else — no prose, no Markdown, no code fences."

// aiActions returns structured, actionable test payloads for a single flow so the
// UI can render them as cards and load them straight into Intruder. Best-effort:
// if the model wraps the JSON in prose or fences, extractJSONArray recovers it; an
// unparseable reply yields an empty list rather than an error.
func (h *aiAPI) aiActions(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in aiAssistReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	ids := in.ids()
	if len(ids) == 0 {
		httpErr(w, http.StatusBadRequest, "no flow selected")
		return
	}
	f, err := h.st.GetFlow(ids[0])
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	req := clip(string(h.rawRequest(f)), 2500)
	prompt := "For the HTTP request below, list up to 12 concrete test payloads a pentester should try (injection, IDOR, auth bypass, path traversal, SSRF, etc.). " +
		"Return ONLY a JSON array of objects, each with keys \"point\" (the parameter or location to inject, e.g. \"query:id\" or \"header:Authorization\"), " +
		"\"payload\" (the exact string to send), \"why\" (one short line), and \"tool\": set it to \"repeater\" for a one-shot manual probe where you send a single crafted request and read the response " +
		"(auth/authz bypass, a specific IDOR value, an SSRF probe, a logic test), or \"intruder\" when the point should be fuzzed or enumerated over many values (wordlists, brute force, ID ranges, injection fuzzing). " +
		"Request:\n\n" + req
	model, _, _ := h.st.GetSetting("ai.model")
	text, err := aiassist.New(provider, key, model).Complete(actionsSystem, prompt)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var payloads []aiPayload
	if arr := extractJSONArray(text); arr != "" {
		_ = json.Unmarshal([]byte(arr), &payloads)
	}
	writeJSON(w, http.StatusOK, map[string]any{"payloads": payloads})
}

// extractJSONArray returns the outermost [...] slice of s, tolerating any prose or
// ```json fences a model may add despite the system prompt.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// assistFlow is one flow's raw text fed to the AI assist.
type assistFlow struct {
	Label    string // "#42 GET https://h/x"
	Req      string
	Res      string
	Findings string // passive findings, for the "summarize" kind
}

// assistPrompt builds the AI-assist user prompt. One flow keeps the original
// focused wording; several selected flows become a combined per-endpoint review.
func assistPrompt(kind string, flows []assistFlow, question string) string {
	question = strings.TrimSpace(question)
	if len(flows) == 1 {
		f := flows[0]
		switch kind {
		case "ask":
			return "Answer this question about the HTTP exchange below, using only what it shows:\n\nQuestion: " + question + "\n\nRequest:\n" + f.Req + "\n\nResponse:\n" + f.Res
		case "suggest":
			return "Suggest specific test payloads (injection, IDOR, auth bypass, etc.) for the parameters in this request, with a one-line rationale each:\n\n" + f.Req
		case "summarize":
			return "Summarize the security posture of this exchange in a few bullets. Passive findings:\n" + f.Findings + "\nRequest:\n" + f.Req + "\n\nResponse:\n" + f.Res
		default:
			return "Explain what this HTTP request/response does and anything security-relevant a tester should check:\n\nRequest:\n" + f.Req + "\n\nResponse:\n" + f.Res
		}
	}
	lead := map[string]string{
		"ask":       "Answer this question about the captured exchanges below, using only what they show:\n\nQuestion: " + question,
		"suggest":   "Across these requests, suggest specific test payloads (injection, IDOR, auth bypass, etc.) worth trying, grouped by endpoint, each with a one-line rationale.",
		"summarize": "Review these captured exchanges together and summarize the security posture and the highest-value things to test, in a few bullets.",
	}[kind]
	if lead == "" {
		lead = "Review these captured exchanges and call out anything security-relevant a tester should check, grouped by endpoint."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%d exchanges:\n", lead, len(flows))
	for i, f := range flows {
		fmt.Fprintf(&b, "\n=== [%d] %s ===\n", i+1, f.Label)
		if f.Findings != "" {
			b.WriteString("Passive findings:\n" + f.Findings)
		}
		b.WriteString("Request:\n" + f.Req + "\n")
		if kind != "suggest" && f.Res != "" {
			b.WriteString("Response:\n" + f.Res + "\n")
		}
	}
	return b.String()
}

const maxAskHistoryTurns = 20

// assistMessages builds the provider message list. Ask follow-ups reuse prior
// turns and only re-send the flow context once at the head of the thread.
func assistMessages(kind string, flows []assistFlow, question string, history []aiAssistTurn) []aiassist.Message {
	if kind == "ask" {
		return buildAskMessages(flows, history, question)
	}
	return []aiassist.Message{{Role: "user", Content: assistPrompt(kind, flows, question)}}
}

func buildAskMessages(flows []assistFlow, history []aiAssistTurn, question string) []aiassist.Message {
	question = strings.TrimSpace(question)
	history = normalizeAskHistory(history)
	if len(history) == 0 {
		return []aiassist.Message{{Role: "user", Content: assistPrompt("ask", flows, question)}}
	}
	msgs := []aiassist.Message{{Role: "user", Content: flowContextPrompt(flows)}}
	for _, t := range history {
		msgs = append(msgs, aiassist.Message{Role: t.Role, Content: t.Content})
	}
	msgs = append(msgs, aiassist.Message{Role: "user", Content: question})
	return msgs
}

func normalizeAskHistory(history []aiAssistTurn) []aiAssistTurn {
	out := make([]aiAssistTurn, 0, len(history))
	for _, t := range history {
		role := strings.TrimSpace(t.Role)
		content := strings.TrimSpace(t.Content)
		if content == "" || (role != "user" && role != "assistant") {
			continue
		}
		out = append(out, aiAssistTurn{Role: role, Content: content})
	}
	max := maxAskHistoryTurns * 2
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// flowContextPrompt anchors a follow-up thread with the captured exchange(s).
func flowContextPrompt(flows []assistFlow) string {
	if len(flows) == 1 {
		f := flows[0]
		return "We are discussing this HTTP exchange. Answer follow-up questions using only what it shows.\n\nRequest:\n" + f.Req + "\n\nResponse:\n" + f.Res
	}
	var b strings.Builder
	fmt.Fprintf(&b, "We are discussing these %d captured exchanges. Answer follow-up questions using only what they show.\n\n", len(flows))
	for i, f := range flows {
		fmt.Fprintf(&b, "\n=== [%d] %s ===\n", i+1, f.Label)
		b.WriteString("Request:\n" + f.Req + "\n")
		if f.Res != "" {
			b.WriteString("Response:\n" + f.Res + "\n")
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n…(truncated)"
	}
	return s
}

// aiOpenRouterModels returns the OpenRouter model catalog and optionally validates
// an API key (?key= for an unsaved key, else stored key / OPENROUTER_API_KEY).
func (h *aiAPI) aiOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		key, _, _ = h.st.GetSetting("ai.apiKey")
		if key == "" {
			key = os.Getenv("OPENROUTER_API_KEY")
		}
	}
	var keyErr string
	if key != "" {
		if err := aiassist.ValidateOpenRouterKey(r.Context(), key); err != nil {
			keyErr = err.Error()
		}
	}
	models, err := aiassist.ListOpenRouterModels(r.Context())
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models":   models,
		"keyValid": key != "" && keyErr == "",
		"keyError": keyErr,
	})
}
