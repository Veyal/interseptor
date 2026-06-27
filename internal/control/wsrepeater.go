package control

import (
	"encoding/json"
	"net/http"

	"github.com/Veyal/interceptor/internal/wsrepeater"
)

// wsSend opens a fresh WebSocket to a target, sends one message, and returns the
// frames the server replies with (a WebSocket Repeater). Optional handshake
// headers are given as "Key: Value" lines, like the session editor.
func (h *Hub) wsSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL     string `json:"url"`
		Message string `json:"message"`
		Binary  bool   `json:"binary"`
		Headers string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.URL == "" {
		httpErr(w, http.StatusBadRequest, "url required")
		return
	}
	if h.targetsOwnListener(in.URL) {
		httpErr(w, http.StatusForbidden, "refusing to send to Interceptor's own listener")
		return
	}
	hdrs := map[string]string{}
	for _, hh := range parseSessionHeaders(in.Headers) {
		hdrs[hh.Key] = hh.Value
	}
	res, err := wsrepeater.Send(wsrepeater.Request{URL: in.URL, Message: in.Message, Binary: in.Binary, Headers: hdrs})
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
