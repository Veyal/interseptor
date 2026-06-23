package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/store"
)

func (h *Hub) listViews(w http.ResponseWriter, r *http.Request) {
	views, err := h.st.ListViews()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if views == nil {
		views = []store.SavedView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": views})
}

func (h *Hub) createView(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Name == "" {
		httpErr(w, http.StatusBadRequest, "name required")
		return
	}
	data := string(in.Data)
	if data == "" {
		data = "{}"
	}
	v := store.SavedView{Name: in.Name, Data: data}
	if _, err := h.st.CreateView(&v); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "views.update"})
	writeJSON(w, http.StatusCreated, v)
}

func (h *Hub) deleteView(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.st.DeleteView(id); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "views.update"})
	w.WriteHeader(http.StatusNoContent)
}
