package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Veyal/interceptor/internal/aiassist"
)

const notesOrganizeSystem = `You reorganize pentest engagement notebooks into rich, scannable Markdown.

Output rules:
- Reply with ONLY valid Markdown — no preamble, no commentary, and do NOT wrap the entire document in a code fence.
- Preserve EVERY fact, credential, token, URL, finding, and to-do. Never invent content.
- Keep image refs like ![alt](/api/notes/images/123) exactly unchanged.

Structure:
# Engagement notes
## Scope
## Credentials
## Findings
## To-do
## Misc (only when needed)

Use full Markdown formatting (not plain bullet dumps):

Credentials:
- Prefer a table: | Role | User | Secret | Notes | (or | Service | Login | Password |)
- Wrap every password, API key, token, cookie value, and hash in inline backticks (e.g. admin / S3cr3t!)
- Multi-line secrets, JWTs, .env snippets, or private keys go in fenced code blocks (triple backticks).

Findings:
- One ### heading per finding.
- Use **Severity:**, **Host/path:**, **Evidence:**, **Status:** labels (bold).
- Put payloads, params, SQLi strings, and header values in inline backticks.
- Use blockquotes (>) for short raw request/response excerpts.
- Separate major findings with --- when helpful.

To-do:
- GitHub-style checkboxes: - [ ] open item and - [x] completed item.

Scope:
- Bullet hosts/IPs; inline backticks for domains, paths, and scope patterns.

General:
- Turn bare URLs into [label](url) links.
- Use numbered lists when order matters.
- Merge duplicates, drop empty sections, normalize heading levels.`

// notesOrganizeReq is the JSON body for notes organize endpoints. When Notes is
// empty the server loads the persisted notebook.
type notesOrganizeReq struct {
	Notes string `json:"notes"`
}

func (h *aiAPI) notesForOrganize(in notesOrganizeReq) (string, error) {
	notes := strings.TrimSpace(in.Notes)
	if notes == "" {
		var err error
		notes, err = h.st.LoadNotes()
		if err != nil {
			return "", err
		}
		notes = strings.TrimSpace(notes)
	}
	if notes == "" {
		return "", fmt.Errorf("notes are empty — write something first")
	}
	return notes, nil
}

func notesOrganizePrompt(notes string) string {
	const max = 16000
	if len(notes) > max {
		notes = notes[:max] + "\n…(truncated)"
	}
	return "Reorganize these project notes into a rich, well-formatted engagement notebook (tables, inline code for secrets, task checkboxes, blockquotes for evidence):\n\n" + notes
}

// extractFencedMarkdown strips optional ``` / ```markdown wrappers models add despite instructions.
func extractFencedMarkdown(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		if j := strings.LastIndex(s, "\n```"); j > i {
			return strings.TrimSpace(s[i+1 : j])
		}
	}
	return strings.Trim(strings.TrimPrefix(s, "```markdown"), "`")
}

// aiNotesOrganize returns reorganized markdown for the project notebook (non-streaming).
func (h *aiAPI) aiNotesOrganize(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in notesOrganizeReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	notes, err := h.notesForOrganize(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	model, _, _ := h.st.GetSetting("ai.model")
	text, err := aiassist.New(provider, key, model).Complete(notesOrganizeSystem, notesOrganizePrompt(notes))
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": extractFencedMarkdown(text)})
}

// aiNotesOrganizeStream streams reorganized markdown token-by-token as SSE.
func (h *aiAPI) aiNotesOrganizeStream(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in notesOrganizeReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	notes, err := h.notesForOrganize(in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	model, _, _ := h.st.GetSetting("ai.model")
	err = aiassist.New(provider, key, model).CompleteStream(r.Context(), notesOrganizeSystem, notesOrganizePrompt(notes), func(delta string) {
		b, _ := json.Marshal(delta)
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
