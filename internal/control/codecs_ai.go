package control

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/aiassist"
	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

//go:embed codecs_reference.md
var codecsReferenceMD []byte

const codecsGenerateSystem = `You write Interseptor message codecs in Starlark (Python-like, sandboxed).

Reply with ONLY valid Starlark source — no markdown fences, no explanation, no imports.

Required shape:
meta = {
    "id": "kebab-case-id",
    "title": "Short title",
    "apply_on_send": False,
}

def match(flow, side):
    # side is "req" or "res"; return True when this codec should handle the body
    return False

def decode(flow, side, raw):
    # return plaintext string OR {"plaintext": "...", "fields": {...}, "note": "..."}
    return raw

# Optional — required only when apply_on_send is True:
# def encode(flow, side, plaintext):
#     return plaintext

The flow object (read-only):
- Fields: method, scheme, host, port, path, status, mime, req_body, res_body, req_headers, res_headers
- Methods: flow.req_header(name), flow.res_header(name), flow.query_param(name)

Builtins (only these — no other functions exist):
- aes_ecb_encrypt(key, plaintext), aes_ecb_decrypt(key, ciphertext)
- hash, hmac, b64encode, b64decode, url_encode, url_decode
- json_encode, json_decode, re_search

Rules:
- No imports, load(), file I/O, network, or clock access.
- Prefer display-only (apply_on_send=False) unless the user asks to re-encode on Repeater send.
- If no codec id was given, put as the first line: # suggested-id: kebab-case-name
- Keep match() selective so unrelated traffic is skipped.`

type codecsGenerateReq struct {
	Description string `json:"description"`
	Source      string `json:"source"`
	FlowID      int64  `json:"flowId"`
}

func codecsGeneratePrompt(desc, existing string, f *store.Flow) string {
	const maxDesc = 4000
	desc = strings.TrimSpace(desc)
	if len(desc) > maxDesc {
		desc = desc[:maxDesc] + "\n…(truncated)"
	}
	var b strings.Builder
	b.WriteString("Write a message codec that:\n\n")
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

func (h *checksAPI) codecsReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"markdown": string(codecsReferenceMD)})
}

// aiCodecsGenerate turns a plain-text description into Starlark codec source (compile-validated).
func (h *aiAPI) aiCodecsGenerate(w http.ResponseWriter, r *http.Request) {
	if h.denyIfAIDisabled(w) {
		return
	}
	provider, key, endpoint, ok := h.aiCreds()
	if !ok {
		httpErr(w, http.StatusBadRequest, aiNoKeyMsg)
		return
	}
	var in codecsGenerateReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		httpErr(w, http.StatusBadRequest, "description is required — describe the wire format / crypto to unwrap")
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
	ai := aiassist.New(provider, key, model, endpoint)
	prompt := codecsGeneratePrompt(desc, in.Source, f)
	raw, err := ai.Complete(codecsGenerateSystem, prompt)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	src := extractCheckSource(raw)
	suggestedID := parseSuggestedID(raw)
	id := suggestedID
	if id == "" {
		id = "gen"
	}
	if _, err := msgcodec.Compile(id, src); err != nil {
		repaired, err2 := ai.Complete(codecsGenerateSystem, prompt+"\n\nYour previous output failed to compile:\n"+err.Error()+"\n\nReply with ONLY corrected Starlark.")
		if err2 != nil {
			writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "source": src, "suggestedId": suggestedID})
			return
		}
		src2 := extractCheckSource(repaired)
		if sid := parseSuggestedID(repaired); sid != "" {
			suggestedID = sid
			id = sid
		}
		if _, err3 := msgcodec.Compile(id, src2); err3 != nil {
			writeJSON(w, http.StatusOK, map[string]any{"error": err3.Error(), "source": src2, "suggestedId": suggestedID})
			return
		}
		src = src2
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "suggestedId": suggestedID})
}
