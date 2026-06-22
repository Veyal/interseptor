package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

type repeaterSendJSON struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Headers string `json:"headers"` // raw "Key: Value" lines
	Body    string `json:"body"`
}

func (h *Hub) repeaterSend(w http.ResponseWriter, r *http.Request) {
	var in repeaterSendJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	flow, err := h.snd.Send(sender.Request{
		Method:  in.Method,
		URL:     in.URL,
		Headers: parseHeaderLines(in.Headers),
		Body:    []byte(in.Body),
		Flags:   store.FlagRepeater,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.flowDetail(flow))
}

func (h *Hub) repeaterHistory(w http.ResponseWriter, r *http.Request) {
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
func (h *Hub) flowDetail(f *store.Flow) flowDetailJSON {
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
