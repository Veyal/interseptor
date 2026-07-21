package control

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/codec"
	"github.com/Veyal/interseptor/internal/msgcodec"
)

const (
	selectionDecodeMin = 4
	selectionDecodeMax = 8192
)

var jwtKindRe = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

// selectionDecode previews a decode for highlighted text: project message codecs
// first (when a flow is given), then built-in smart decode.
func (h *toolsAPI) selectionDecode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Input  string `json:"input"`
		FlowID int64  `json:"flowId"`
		Side   string `json:"side"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	input := strings.TrimSpace(in.Input)
	if len(input) < selectionDecodeMin || len(input) > selectionDecodeMax {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false})
		return
	}
	side := in.Side
	if side == "" {
		side = "req"
	}

	if out, ok := h.selectionDecodeCodec(input, in.FlowID, side); ok {
		writeJSON(w, http.StatusOK, out)
		return
	}

	smartOut, err := codec.Apply("smart", input)
	if err != nil || smartOut == "" || smartOut == input {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matched": true,
		"kind":    encodeKindLabel(input),
		"output":  smartOut,
	})
}

func (h *toolsAPI) selectionDecodeCodec(input string, flowID int64, side string) (map[string]any, bool) {
	if flowID <= 0 || h.ProjectDir == "" {
		return nil, false
	}
	codecs := h.loadCodecs()
	if len(codecs) == 0 {
		return nil, false
	}
	f, err := h.st.GetFlow(flowID)
	if err != nil || f == nil {
		return nil, false
	}
	flow := h.flowForCodec(f)

	// A. Try selection as the whole body (codecs that accept a bare payload).
	bare := flow
	if side == "res" {
		bare.ResBody = input
	} else {
		bare.ReqBody = input
	}
	if res, matched := msgcodec.TryDecode(codecs, bare, side); matched && res.Error == "" {
		out := strings.TrimSpace(res.Plaintext)
		if out != "" && out != input {
			return codecPreview(res, out), true
		}
	}

	// B. Full-body decode, then map selection → plaintext / fields.
	res, matched := msgcodec.TryDecode(codecs, flow, side)
	if !matched || res.Error != "" {
		return nil, false
	}
	body := bodyForSide(flow, side)
	preview, ok := mapSelectionToCodecPreview(input, body, res)
	if !ok {
		return nil, false
	}
	return codecPreview(res, preview), true
}

func codecPreview(res msgcodec.Result, output string) map[string]any {
	kind := res.Title
	if kind == "" {
		kind = res.CodecID
	}
	out := map[string]any{
		"matched": true,
		"kind":    kind,
		"output":  output,
		"codecId": res.CodecID,
		"title":   res.Title,
	}
	if res.Note != "" {
		out["note"] = res.Note
	}
	return out
}

func bodyForSide(flow msgcodec.Flow, side string) string {
	if side == "res" || side == "response" {
		return flow.ResBody
	}
	return flow.ReqBody
}

// mapSelectionToCodecPreview picks plaintext for a selection against a decoded body.
func mapSelectionToCodecPreview(sel, body string, res msgcodec.Result) (string, bool) {
	sel = strings.TrimSpace(sel)
	bodyTrim := strings.TrimSpace(body)
	if sel == "" || res.Plaintext == "" {
		return "", false
	}
	if sel == bodyTrim || sel == body {
		out := strings.TrimSpace(res.Plaintext)
		if out != "" && out != sel {
			return out, true
		}
		return "", false
	}
	if key, ok := jsonStringKeyForValue(body, sel); ok {
		if v, found := res.Fields[key]; found {
			v = strings.TrimSpace(v)
			if v != "" && v != sel {
				return v, true
			}
		}
		// Single-field codec: use that field even if key names differ slightly.
		if len(res.Fields) == 1 {
			for _, v := range res.Fields {
				v = strings.TrimSpace(v)
				if v != "" && v != sel {
					return v, true
				}
			}
		}
	}
	return "", false
}

// jsonStringKeyForValue returns the object key whose string value equals want.
func jsonStringKeyForValue(raw, want string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", false
	}
	for k, v := range obj {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s == want {
			return k, true
		}
	}
	return "", false
}

// encodeKindLabel mirrors the UI helper for built-in smart decode.
func encodeKindLabel(s string) string {
	t := strings.TrimSpace(s)
	if jwtKindRe.MatchString(t) {
		return "JWT"
	}
	if strings.Contains(t, "%") {
		return "URL"
	}
	if len(t) >= 4 && len(t)%2 == 0 {
		hexOK := true
		for _, r := range t {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				hexOK = false
				break
			}
		}
		if hexOK {
			return "Hex"
		}
	}
	if len(t) >= 8 {
		b64OK := true
		for _, r := range t {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
				r == '+' || r == '/' || r == '-' || r == '_' || r == '=') {
				b64OK = false
				break
			}
		}
		if b64OK {
			return "Base64"
		}
	}
	return "Decoded"
}
