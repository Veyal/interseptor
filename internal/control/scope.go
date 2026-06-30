package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/scope"
	"github.com/Veyal/interceptor/internal/store"
)

// refreshScope reloads the live scope matcher from the store and announces it.
func (h *Hub) refreshScope() {
	rules, err := h.st.ListScopeRules()
	if err == nil {
		h.sc.SetRules(rules)
	}
	h.broadcast(map[string]any{"type": "scope.update"})
}

func (h *scopeAPI) listScope(w http.ResponseWriter, r *http.Request) {
	rules, err := h.st.ListScopeRules()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []store.ScopeRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func validScope(w http.ResponseWriter, r store.ScopeRule) bool {
	if r.Action != "include" && r.Action != "exclude" {
		httpErr(w, http.StatusBadRequest, "action must be include or exclude")
		return false
	}
	if r.Host == "" && r.Path == "" && r.Scheme == "" && r.Port == 0 {
		httpErr(w, http.StatusBadRequest, "a scope rule needs at least one of host/path/scheme/port")
		return false
	}
	if err := scope.ValidateRule(r); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func (h *scopeAPI) createScope(w http.ResponseWriter, r *http.Request) {
	var in store.ScopeRule
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !validScope(w, in) {
		return
	}
	if _, err := h.st.CreateScopeRule(&in); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.refreshScope()
	writeJSON(w, http.StatusCreated, in)
}

func (h *scopeAPI) updateScope(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in store.ScopeRule
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	in.ID = id
	if !validScope(w, in) {
		return
	}
	if err := h.st.UpdateScopeRule(&in); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.refreshScope()
	writeJSON(w, http.StatusOK, in)
}

func (h *scopeAPI) deleteScope(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteScopeRule(id); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.refreshScope()
	w.WriteHeader(http.StatusNoContent)
}
