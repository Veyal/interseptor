package control

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/oob"
)

const maxOOBConfigRequestBytes int64 = 16 << 10

// oobBase returns the configured public base URL for OOB payloads, falling back
// to this request's own origin (loopback — fine for local self-testing only).
func (h *oobAPI) oobBase(r *http.Request) string {
	base, _ := h.oobConfiguredBase()
	if base == "" {
		base = "http://" + r.Host + "/oob"
	}
	return strings.TrimRight(base, "/")
}

// oobConfiguredBase returns the operator-set base URL and whether one is set.
func (h *oobAPI) oobConfiguredBase() (string, bool) {
	base, _, _ := h.st.GetSetting("oob.baseUrl")
	return base, base != ""
}

// validOOBBaseURL accepts an absolute http(s) URL with a host and no query or
// fragment, since the token is appended as a path segment.
func validOOBBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return errors.New("base URL must be an absolute http:// or https:// URL, e.g. https://cb.example.com/oob")
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return errors.New("base URL must not contain a query or fragment — the token is appended as a path segment")
	}
	return nil
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
	_, configured := h.oobConfiguredBase()
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl":      h.oobBase(r),
		"configured":   configured,
		"interactions": h.oob.List(),
		"limit":        oob.MaxInteractions,
		"dropped":      h.oob.Dropped(),
		"retention":    "memory",
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
	if !decodeLimitedJSON(w, r, maxOOBConfigRequestBytes, &in) {
		return
	}
	base := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if base != "" {
		if err := validOOBBaseURL(base); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := h.st.SetSetting("oob.baseUrl", base); err != nil {
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
