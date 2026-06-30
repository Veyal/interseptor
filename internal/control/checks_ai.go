package control

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Veyal/interceptor/internal/aiassist"
	"github.com/Veyal/interceptor/internal/checkscript"
	"github.com/Veyal/interceptor/internal/store"
)

//go:embed checks_reference.md
var checksReferenceMD []byte

const checksGenerateSystem = `You write Interceptor passive scanner checks in Starlark (Python-like, sandboxed).

Reply with ONLY valid Starlark source — no markdown fences, no explanation, no imports.

Required shape:
def check(flow):
    # inspect flow; return a list of finding(...) or []
    return []

The flow object (read-only):
- Fields: method, scheme, host, port, path, status, mime, req_body, res_body, req_headers, res_headers
- Methods: flow.req_header(name), flow.res_header(name), flow.query_param(name) — header lookup is case-insensitive; missing → ""

Builtins (only these — no other functions exist):
- finding(severity, title, detail="", evidence="", fix="") — severity is high, medium, low, or info
- re_search(pattern, text) — first regex match (RE2) or None

Rules:
- Return a list of finding(...) or [] (or None) when nothing matches.
- No imports, load(), file I/O, network, or clock access.
- Use only the flow API above — do not invent fields or helpers.
- Prefer early returns; keep checks fast and readable.
- If no check id was given in the request, put as the first line: # suggested-id: kebab-case-name`

type checksGenerateReq struct {
	Description string `json:"description"`
	Source      string `json:"source"`
	FlowID      int64  `json:"flowId"`
}

var suggestedIDRe = regexp.MustCompile(`(?m)^#\s*suggested-id:\s*([a-zA-Z0-9][a-zA-Z0-9_-]*)\s*$`)

func checksGeneratePrompt(desc, existing string, f *store.Flow) string {
	const maxDesc = 4000
	desc = strings.TrimSpace(desc)
	if len(desc) > maxDesc {
		desc = desc[:maxDesc] + "\n…(truncated)"
	}
	var b strings.Builder
	b.WriteString("Write a passive scanner check that:\n\n")
	b.WriteString(desc)
	if strings.TrimSpace(existing) != "" {
		const maxSrc = 8000
		src := existing
		if len(src) > maxSrc {
			src = src[:maxSrc] + "\n…(truncated)"
		}
		b.WriteString("\n\nRefine this existing draft:\n\n")
		b.WriteString(src)
	}
	if f != nil {
		b.WriteString(fmt.Sprintf("\n\nExample captured flow (for context):\n%s %s://%s:%d%s → %d %s\n",
			f.Method, f.Scheme, f.Host, f.Port, f.Path, f.Status, f.Mime))
	}
	return b.String()
}

// extractCheckSource strips optional markdown fences and suggested-id comment lines.
func extractCheckSource(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			if j := strings.LastIndex(s, "\n```"); j > i {
				s = strings.TrimSpace(s[i+1 : j])
			}
		}
	}
	if strings.HasPrefix(s, "def check(") {
		return stripSuggestedIDComment(s)
	}
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			return stripSuggestedIDComment(strings.TrimSpace(rest[:k]))
		}
	}
	return stripSuggestedIDComment(s)
}

func stripSuggestedIDComment(s string) string {
	return strings.TrimSpace(suggestedIDRe.ReplaceAllString(s, ""))
}

func parseSuggestedID(s string) string {
	if m := suggestedIDRe.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return ""
}

func (h *checksAPI) checksReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"markdown": string(checksReferenceMD)})
}

// aiChecksGenerate turns a plain-text description into Starlark check source (compile-validated).
func (h *aiAPI) aiChecksGenerate(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in checksGenerateReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		httpErr(w, http.StatusBadRequest, "description is required — describe what the check should detect")
		return
	}
	var f *store.Flow
	if in.FlowID > 0 {
		got, err := h.st.GetFlow(in.FlowID)
		if err == nil {
			f = got
		}
	} else if flows, _ := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 1}); len(flows) > 0 {
		f = flows[0]
	}
	model, _, _ := h.st.GetSetting("ai.model")
	ai := aiassist.New(provider, key, model)
	prompt := checksGeneratePrompt(desc, in.Source, f)
	raw, err := ai.Complete(checksGenerateSystem, prompt)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	src := extractCheckSource(raw)
	suggestedID := parseSuggestedID(raw)
	if _, err := checkscript.Compile("gen", src); err != nil {
		repaired, err2 := ai.Complete(checksGenerateSystem, prompt+"\n\nYour previous output failed to compile:\n"+err.Error()+"\n\nReply with ONLY corrected Starlark.")
		if err2 != nil {
			writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "source": src, "suggestedId": suggestedID})
			return
		}
		src2 := extractCheckSource(repaired)
		if id := parseSuggestedID(repaired); id != "" {
			suggestedID = id
		}
		if _, err3 := checkscript.Compile("gen", src2); err3 != nil {
			writeJSON(w, http.StatusOK, map[string]any{"error": err3.Error(), "source": src2, "suggestedId": suggestedID})
			return
		}
		src = src2
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "suggestedId": suggestedID})
}
