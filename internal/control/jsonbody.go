package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// decodeLimitedJSON decodes exactly one JSON value while enforcing an endpoint-
// specific request-body limit. A second Decode is intentional: it consumes
// trailing whitespace, detects extra JSON values, and forces MaxBytesReader to
// report padding that extends beyond the limit after an otherwise-valid value.
func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		writeJSONDecodeError(w, err)
		return false
	}

	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			httpErr(w, http.StatusBadRequest, "bad json")
		} else {
			writeJSONDecodeError(w, err)
		}
		return false
	}
	return true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	httpErr(w, http.StatusBadRequest, "bad json")
}
