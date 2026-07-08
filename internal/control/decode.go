package control

import (
	"encoding/json"
	"net/http"

	"github.com/Veyal/interseptor/internal/codec"
)

// decode runs one encode/decode transform over the input. A bad input returns
// 200 with an {error} field (not 500) so the UI can show it inline.
func (h *toolsAPI) decode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Op    string `json:"op"`
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	out, err := codec.Apply(in.Op, in.Input)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out})
}
