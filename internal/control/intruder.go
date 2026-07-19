package control

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Veyal/interseptor/internal/intruder"
)

type intruderStartJSON struct {
	Target       string     `json:"target"`
	Template     string     `json:"template"`
	AttackType   string     `json:"attackType"`
	Payloads     [][]string `json:"payloads"`
	Repeat       int        `json:"repeat"`
	Threads      int        `json:"threads"`
	DelayMs      int        `json:"delayMs"`
	GrepMatch    string     `json:"grepMatch"`
	GrepExtract  string     `json:"grepExtract"`
	ProcessRules []string   `json:"processRules"`
}

func (h *toolsAPI) intruderStart(w http.ResponseWriter, r *http.Request) {
	var in intruderStartJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if h.targetsOwnListener(in.Target) {
		httpErr(w, http.StatusForbidden, "refusing to attack Interseptor's own listener")
		return
	}
	if in.Threads <= 0 {
		httpErr(w, http.StatusBadRequest, "threads must be a positive number")
		return
	}
	err := h.intr.Start(intruder.Spec{
		Target:       in.Target,
		Template:     in.Template,
		AttackType:   in.AttackType,
		Payloads:     in.Payloads,
		Repeat:       in.Repeat,
		Threads:      in.Threads,
		DelayMs:      in.DelayMs,
		GrepMatch:    in.GrepMatch,
		GrepExtract:  in.GrepExtract,
		ProcessRules: in.ProcessRules,
		ExtraFlags:   aiSourceFlag(r),
	})
	if err != nil {
		if errors.Is(err, intruder.ErrClosed) {
			httpErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h.intr.State())
}

func (h *toolsAPI) intruderState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.intr.State())
}
