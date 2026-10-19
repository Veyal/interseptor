package control

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Held messages travel to the editor as a JSON string and come back from a
// <textarea>. A body that is not text cannot survive that: invalid UTF-8 turns
// into U+FFFD and every CR into LF. Such a body is kept on the server instead —
// the editor gets the head only, and Forward with keepBody splices the edited
// head onto the original body bytes.

// heldParts splits a raw held message at its first blank line (CRLF or LF).
func heldParts(raw []byte) (head, body []byte, ok bool) {
	best, off := -1, 0
	for _, sep := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
		if i := bytes.Index(raw, sep); i >= 0 && (best < 0 || i < best) {
			best, off = i, i+len(sep)
		}
	}
	if best < 0 {
		return raw, nil, false
	}
	return raw[:best], raw[off:], true
}

// heldBodyBinary reports whether the body would be corrupted by the editor.
func heldBodyBinary(body []byte) bool {
	return len(body) > 0 && (!utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0)
}

// heldView is what the editor receives for one held message.
type heldView struct {
	Raw         string `json:"raw"`
	Binary      bool   `json:"binary,omitempty"`
	BodyLen     int    `json:"bodyLen,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

func heldViewOf(raw []byte) heldView {
	head, body, ok := heldParts(raw)
	v := heldView{Raw: string(raw), ContentType: heldContentType(head)}
	if ok && heldBodyBinary(body) {
		v.Raw, v.Binary, v.BodyLen = string(head)+"\r\n\r\n", true, len(body)
	}
	return v
}

// heldContentType returns the Content-Type header value of a raw head.
func heldContentType(head []byte) string {
	for _, line := range strings.Split(string(head), "\n") {
		name, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "content-type") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// spliceHeldHead joins the edited head (anything after its first blank line is
// ignored) to the original body, with CRLF line endings in the head.
func spliceHeldHead(edited string, original []byte) []byte {
	head, _, _ := heldParts([]byte(edited))
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(string(head), "\r\n"), "\r\n", "\n"), "\n")
	_, body, _ := heldParts(original)
	out := []byte(strings.Join(lines, "\r\n") + "\r\n\r\n")
	return append(out, body...)
}

// heldRawByID returns the held bytes of one request or response.
func (h *interceptAPI) heldRawByID(id int64, resp bool) ([]byte, bool) {
	if resp {
		for _, held := range h.eng.ResponseQueue() {
			if held.ID == id {
				return held.Raw, true
			}
		}
		return nil, false
	}
	for _, held := range h.eng.Queue() {
		if held.ID == id {
			return held.Raw, true
		}
	}
	return nil, false
}

// forwardRaw is the edited message to forward: the text as sent, or with
// keepBody the edited head on the held body bytes.
func (h *interceptAPI) forwardRaw(id int64, resp bool, raw string, keepBody bool) []byte {
	if raw == "" || !keepBody {
		return []byte(raw)
	}
	original, ok := h.heldRawByID(id, resp)
	if !ok {
		return []byte(raw) // not held: the engine reports it
	}
	return spliceHeldHead(raw, original)
}

// previewableImage lists the raster types the held body endpoint serves as
// themselves; anything else (HTML, SVG, scripts) is served as an attachment of
// application/octet-stream so it can never run in the control origin.
func previewableImage(ct string) string {
	base := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch base {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon":
		return base
	}
	return ""
}

// getInterceptHeldBody serves the body bytes of a held message, for the
// editor's image preview.
func (h *interceptAPI) getInterceptHeldBody(w http.ResponseWriter, r *http.Request) {
	if h.eng == nil {
		httpErr(w, http.StatusNotImplemented, "intercept unavailable")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	raw, ok := h.heldRawByID(id, r.URL.Query().Get("side") == "resp")
	if !ok {
		httpErr(w, http.StatusNotFound, "not held")
		return
	}
	head, body, _ := heldParts(raw)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if ct := previewableImage(heldContentType(head)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="held-`+strconv.FormatInt(id, 10)+`.bin"`)
	}
	_, _ = w.Write(body)
}
