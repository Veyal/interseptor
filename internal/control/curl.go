package control

import (
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/curlgen"
)

// flowCurl renders a captured flow's request as a runnable curl command, so a
// tester (or the AI) can reproduce it in a terminal.
func (h *flowAPI) flowCurl(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	f, err := h.st.GetFlow(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "flow not found")
		return
	}
	cmd := curlgen.Build(f.Method, analyzeURL(f), http.Header(f.ReqHeaders), h.bodyBytes(f.ReqBodyHash))
	writeJSON(w, http.StatusOK, map[string]any{"curl": cmd})
}
