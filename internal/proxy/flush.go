package proxy

import (
	"errors"
	"mime"
	"net/http"
	"reflect"
	"strings"
)

// applyResponseHeaderRulesInPlace runs enabled res-header rules against
// resp.Header without touching the body, so the body still streams. Framing
// is owned by resp.ContentLength/TransferEncoding: a rule can never add,
// drop, or alter Content-Length / Transfer-Encoding here, which would desync
// the forwarded framing from the untouched body.
func (s *Server) applyResponseHeaderRulesInPlace(flow *proxyFlow, resp *http.Response) {
	orig := resp.Header.Clone()
	h := s.eng.ApplyResponseHeaderRules(resp.Header.Clone())
	for _, k := range []string{"Content-Length", "Transfer-Encoding"} {
		h.Del(k)
		if v, ok := orig[k]; ok {
			h[k] = v
		}
	}
	if !reflect.DeepEqual(orig, h) {
		flow.OriginalResHeaders = cloneHeaders(orig)
	}
	resp.Header = h
}

// flushWriter forwards each write to an http.ResponseWriter and flushes it
// immediately, so incrementally produced bodies (SSE, long-poll, chunked
// progress streams) reach the client as they arrive instead of sitting in the
// server's (HTTP/1.1 or HTTP/2) write buffer until the response completes.
type flushWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
	ok bool // false once the writer reported it cannot flush
}

func newFlushWriter(w http.ResponseWriter) *flushWriter {
	return &flushWriter{w: w, rc: http.NewResponseController(w), ok: true}
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		f.flush()
	}
	return n, err
}

// flush pushes buffered bytes to the client. ErrNotSupported is ignored (the
// writer simply can't flush); other flush errors surface on the next Write.
func (f *flushWriter) flush() {
	if !f.ok {
		return
	}
	if err := f.rc.Flush(); errors.Is(err, http.ErrNotSupported) {
		f.ok = false
	}
}

// isStreamingResponse reports whether a response is an open-ended stream that
// must never be buffered before forwarding (body rules / response hold are
// skipped for it; header rules still apply). Heuristic, kept deliberately
// narrow:
//   - text/event-stream is always a stream (SSE), whatever its framing;
//   - other well-known incremental media types (multipart/x-mixed-replace,
//     NDJSON / JSON-seq) count only when the length is unknown (chunked or
//     close-delimited), since a sized body of those types is finite.
func isStreamingResponse(resp *http.Response) bool {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	mt = strings.ToLower(mt)
	if mt == "text/event-stream" {
		return true
	}
	if resp.ContentLength >= 0 {
		return false
	}
	switch mt {
	case "multipart/x-mixed-replace", "application/x-ndjson", "application/ndjson",
		"application/stream+json", "application/json-seq":
		return true
	}
	return false
}
