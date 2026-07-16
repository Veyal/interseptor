package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// oobBase returns the configured public base URL for OOB payloads, falling back
// to this request's own origin (loopback — fine for local self-testing only).
func (h *oobAPI) oobBase(r *http.Request) string {
	base, _, _ := h.st.GetSetting("oob.baseUrl")
	if base == "" {
		base = "http://" + r.Host + "/oob"
	}
	return strings.TrimRight(base, "/")
}

// oobCatch records a blind out-of-band callback. It is public (the security guard
// lets /oob/ through) and only stores request metadata, returning a tiny response.
func (h *oobAPI) oobCatch(w http.ResponseWriter, r *http.Request) {
	if !h.oobEnabled() {
		http.NotFound(w, r)
		return
	}
	prev := ""
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 512))
		prev = string(b)
	}
	h.oob.Record(r, prev)
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

// oobState returns the current base URL and recorded interactions.
func (h *oobAPI) oobState(w http.ResponseWriter, r *http.Request) {
	if h.denyIfOOBDisabled(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl":      h.oobBase(r),
		"interactions": h.oob.List(),
	})
}

// oobNew mints a fresh token and returns a ready-to-paste payload URL.
func (h *oobAPI) oobNew(w http.ResponseWriter, r *http.Request) {
	if h.denyIfOOBDisabled(w) {
		return
	}
	tok := h.oob.Token()
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "url": h.oobBase(r) + "/" + tok})
}

// oobSetBase persists the public base URL (operator sets a target-reachable host).
func (h *oobAPI) oobSetBase(w http.ResponseWriter, r *http.Request) {
	if h.denyIfOOBDisabled(w) {
		return
	}
	var in struct {
		BaseURL string `json:"baseUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := h.st.SetSetting("oob.baseUrl", strings.TrimSpace(in.BaseURL)); err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"baseUrl": h.oobBase(r)})
}

func (h *oobAPI) oobClear(w http.ResponseWriter, r *http.Request) {
	if h.denyIfOOBDisabled(w) {
		return
	}
	h.oob.Clear()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
