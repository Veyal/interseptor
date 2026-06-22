package control

import (
	"encoding/json"
	"net/http"

	"github.com/Veyal/interceptor/internal/intruder"
)

type intruderStartJSON struct {
	Target     string     `json:"target"`
	Template   string     `json:"template"`
	AttackType string     `json:"attackType"`
	Payloads   [][]string `json:"payloads"`
}

func (h *Hub) intruderStart(w http.ResponseWriter, r *http.Request) {
	var in intruderStartJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	err := h.intr.Start(intruder.Spec{
		Target:     in.Target,
		Template:   in.Template,
		AttackType: in.AttackType,
		Payloads:   in.Payloads,
	})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.intr.State())
}

func (h *Hub) intruderState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.intr.State())
}
