package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/aiassist"
	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

// Clip budgets for Intruder payload generation prompts (issue #16).
const (
	intruderClipReq    = 2500
	intruderClipRes    = 1500
	intruderClipHeader = 400
	intruderMaxPayload = 100
)

// intruderPayloadsSystem forces a bare-JSON object reply for structured Intruder lists.
const intruderPayloadsSystem = "You are a web-app security testing assistant. Reply with ONLY a JSON object and nothing else — no prose, no Markdown, no code fences."

type aiIntruderPayloadsReq struct {
	FlowID int64  `json:"flowId"`
	Hint   string `json:"hint"`
}

type aiIntruderPosition struct {
	Point        string   `json:"point"`
	Marker       string   `json:"marker,omitempty"`
	Payloads     []string `json:"payloads"`
	Why          string   `json:"why,omitempty"`
	ProcessRules []string `json:"processRules,omitempty"`
}

type aiIntruderPayloadsOut struct {
	AttackType   string               `json:"attackType,omitempty"`
	Template     string               `json:"template,omitempty"`
	TemplateHint string               `json:"templateHint,omitempty"`
	Positions    []aiIntruderPosition `json:"positions"`
}

// buildIntruderPayloadsPrompt assembles a sectioned, budgeted prompt from a flow.
func (h *aiAPI) buildIntruderPayloadsPrompt(f *store.Flow, hint string) string {
	reqRaw := clip(string(h.rawRequest(f)), intruderClipReq)
	resRaw := clip(string(h.rawResponse(f)), intruderClipRes)
	u := analyzeURL(f)
	qParams := queryParamNames(f.Path)
	bodyNames := bodyFieldNames(string(h.bodyBytes(f.ReqBodyHash)), http.Header(f.ReqHeaders).Get("Content-Type"))
	reqHdr := pickHeaders(f.ReqHeaders, reqNotableHeaders)
	resHdr := pickHeaders(f.ResHeaders, resNotableHeaders)

	var findings []string
	for _, is := range scanner.AnalyzeWithDisabled(f, h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash), h.checksDisabledSet()) {
		findings = append(findings, is.Severity+": "+is.Title)
	}

	var b strings.Builder
	b.WriteString("Propose Intruder-ready payload lists for the HTTP exchange below.\n")
	b.WriteString("Return ONLY a JSON object with keys:\n")
	b.WriteString(`  "attackType": "sniper" (preferred) | "battering" | "pitchfork" | "cluster",` + "\n")
	b.WriteString(`  "template": optional raw HTTP request with §…§ already around the chosen value(s),` + "\n")
	b.WriteString(`  "templateHint": short note if template omitted,` + "\n")
	b.WriteString(`  "positions": array of { "point": "query:id"|"body:email"|"header:X"|"cookie:sid"|"path:…", "marker": "original or placeholder", "payloads": ["…"], "why": "one line", "processRules": [] }` + "\n")
	fmt.Fprintf(&b, "Prefer lists (many values) suitable for Intruder. Cap each payloads array at %d unless the user hint asks for more (hard cap still applies).\n", intruderMaxPayload)
	b.WriteString("Do not suggest starting the attack — only propose lists and § positions.\n\n")

	if strings.TrimSpace(hint) != "" {
		b.WriteString("User hint: " + strings.TrimSpace(hint) + "\n\n")
	}

	fmt.Fprintf(&b, "=== Summary ===\nMethod: %s\nURL: %s\nStatus: %d\n", f.Method, u, f.Status)
	if len(qParams) > 0 {
		b.WriteString("Query params: " + strings.Join(qParams, ", ") + "\n")
	}
	if len(bodyNames) > 0 {
		b.WriteString("Body fields: " + strings.Join(bodyNames, ", ") + "\n")
	}
	if len(reqHdr) > 0 {
		b.WriteString("Notable request headers:\n")
		for _, k := range reqNotableHeaders {
			if v, ok := reqHdr[k]; ok {
				fmt.Fprintf(&b, "  %s: %s\n", k, clip(v, intruderClipHeader))
			}
		}
	}
	if len(resHdr) > 0 {
		b.WriteString("Notable response headers:\n")
		for _, k := range resNotableHeaders {
			if v, ok := resHdr[k]; ok {
				fmt.Fprintf(&b, "  %s: %s\n", k, clip(v, intruderClipHeader))
			}
		}
	}
	if len(findings) > 0 {
		b.WriteString("Passive scanner hits:\n  " + strings.Join(findings, "\n  ") + "\n")
	}
	b.WriteString("\n=== Request ===\n" + reqRaw + "\n")
	b.WriteString("\n=== Response ===\n" + resRaw + "\n")
	return b.String()
}

var jsonKeyRE = regexp.MustCompile(`"([^"\\]{1,64})"\s*:`)

func bodyFieldNames(body, contentType string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	ct := strings.ToLower(contentType)
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
		if len(out) >= 40 {
			return
		}
	}
	if strings.Contains(ct, "application/x-www-form-urlencoded") || (!strings.Contains(ct, "json") && strings.Contains(body, "=") && !strings.HasPrefix(body, "{")) {
		if vals, err := url.ParseQuery(body); err == nil {
			for k := range vals {
				add(k)
				if len(out) >= 40 {
					return out
				}
			}
			return out
		}
	}
	if strings.Contains(ct, "json") || strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		for _, m := range jsonKeyRE.FindAllStringSubmatch(body, 80) {
			add(m[1])
			if len(out) >= 40 {
				break
			}
		}
	}
	return out
}

// extractJSONObject returns the outermost {...} slice of s.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// aiIntruderPayloads returns structured Intruder payload lists for a flow (#16).
func (h *aiAPI) aiIntruderPayloads(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, endpoint, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in aiIntruderPayloadsReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 {
		httpErr(w, http.StatusBadRequest, "flowId required")
		return
	}
	f, err := h.st.GetFlow(in.FlowID)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	prompt := h.buildIntruderPayloadsPrompt(f, in.Hint)
	model, _, _ := h.st.GetSetting("ai.model")
	text, err := aiassist.New(provider, key, model, endpoint).Complete(intruderPayloadsSystem, prompt)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var out aiIntruderPayloadsOut
	if obj := extractJSONObject(text); obj != "" {
		_ = json.Unmarshal([]byte(obj), &out)
	}
	// Cap list sizes server-side.
	for i := range out.Positions {
		if len(out.Positions[i].Payloads) > intruderMaxPayload {
			out.Positions[i].Payloads = out.Positions[i].Payloads[:intruderMaxPayload]
		}
	}
	if out.AttackType == "" {
		out.AttackType = "sniper"
	}
	writeJSON(w, http.StatusOK, out)
}
