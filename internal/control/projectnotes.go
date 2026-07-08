package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// getNotes returns the project's markdown notebook — a per-project scratchpad for
// credentials, findings, scope notes and to-dos, editable in the UI and by the AI.
// Legacy inline data-URL images are migrated to SQLite-backed refs on read.
func (h *projectAPI) getNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := h.st.LoadNotes()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

// putNotes replaces the project's markdown notebook and tells every client to refresh.
// Inline data-URL images are extracted into SQLite; unreferenced images are GC'd.
func (h *projectAPI) putNotes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Notes string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if _, err := h.st.PersistNotes(in.Notes); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "notes.update"})
	w.WriteHeader(http.StatusNoContent)
}

// patchNotes atomically appends a markdown block to the project notebook,
// entirely inside the store (see Store.AppendNote) — unlike putNotes, this
// avoids the client-side GET-then-PUT lost-update race: two concurrent
// appenders (two AI agents, or an agent racing a human editing in the UI) can
// no longer clobber each other, since there is no client-observable gap
// between reading the current notes and writing the appended result.
func (h *projectAPI) patchNotes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppendText string `json:"appendText"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(in.AppendText) == "" {
		httpErr(w, http.StatusBadRequest, "appendText is required (a non-empty string)")
		return
	}
	if err := h.st.AppendNote(in.AppendText); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "notes.update"})
	w.WriteHeader(http.StatusNoContent)
}

// postNotesImage stores one pasted notebook image and returns its id for markdown refs.
func (h *projectAPI) postNotesImage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mime string `json:"mime"`
		Data string `json:"data"` // raw base64 or a data: URL
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	mime, raw, err := store.DecodeNotesImagePayload(in.Mime, in.Data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.st.InsertNotesImage(mime, raw)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// getNotesImage serves a stored notebook image for preview rendering.
func (h *projectAPI) getNotesImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mime, data, err := h.st.GetNotesImage(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "image not found")
		return
	}
	// Coerce to an allowlisted raster type and forbid MIME sniffing so a stored
	// image can never be served as active content (HTML/SVG/script) in the
	// control-plane origin — see store.SanitizeNotesImageMIME.
	w.Header().Set("Content-Type", store.SanitizeNotesImageMIME(mime))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(data)
}
