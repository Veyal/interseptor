package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/httplines"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

type repeaterSendJSON struct {
	Method  string          `json:"method"`
	URL     string          `json:"url"`
	Headers json.RawMessage `json:"headers"` // "Key: Value" lines or {"Key":"Value"} object
	Body    string          `json:"body"`
}

// aiSourceFlag returns store.FlagAI when a request was issued by the AI assistant
// over MCP — the MCP server stamps every control call with X-Interseptor-Source:
// ai. It lets the control plane distinguish AI-originated Repeater/Intruder/scan
// sends from a human's and surface them in Proxy/History.
func aiSourceFlag(r *http.Request) int64 {
	if strings.EqualFold(r.Header.Get("X-Interseptor-Source"), "ai") {
		return store.FlagAI
	}
	return 0
}

func (h *toolsAPI) repeaterSend(w http.ResponseWriter, r *http.Request) {
	var in repeaterSendJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if h.targetsOwnListener(in.URL) {
		httpErr(w, http.StatusForbidden, "refusing to send to Interseptor's own listener")
		return
	}
	hdr, err := httplines.NormalizeJSON(in.Headers)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	flow, err := h.snd.Send(sender.Request{
		Method:  in.Method,
		URL:     in.URL,
		Headers: hdr,
		Body:    []byte(in.Body),
		Flags:   store.FlagRepeater | aiSourceFlag(r),
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.flowDetail(flow))
}

func (h *toolsAPI) repeaterHistory(w http.ResponseWriter, r *http.Request) {
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{
		RequireFlags: store.FlagRepeater,
		Limit:        atoiOr(r.URL.Query().Get("limit"), 100),
	})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]flowJSON, 0, len(flows))
	for _, f := range flows {
		out = append(out, toFlowJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": out})
}

// flowDetail builds the detail DTO for a freshly-sent flow.
func (h *toolsAPI) flowDetail(f *store.Flow) flowDetailJSON {
	return flowDetailJSON{
		flowJSON:    toFlowJSON(f),
		HTTPVersion: f.HTTPVersion,
		ReqHeaders:  f.ReqHeaders,
		ResHeaders:  f.ResHeaders,
		ReqBodyHash: f.ReqBodyHash,
		ResBodyHash: f.ResBodyHash,
	}
}

// parseHeaderLines turns a raw "Key: Value" block into a header map.
func parseHeaderLines(s string) map[string][]string {
	h := map[string][]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		h[k] = append(h[k], v)
	}
	return h
}
