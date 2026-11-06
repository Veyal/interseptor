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
	return decodeLimitedJSONWithOptions(w, r, limit, dst, false, false, "")
}

// decodeLimitedJSONTooLarge is decodeLimitedJSON with an endpoint-specific
// explanation for a body over the limit, so a 413 says what to do about it
// instead of the generic "request body too large".
func decodeLimitedJSONTooLarge(w http.ResponseWriter, r *http.Request, limit int64, dst any, msg string) bool {
	return decodeLimitedJSONWithOptions(w, r, limit, dst, false, false, msg)
}

func decodeLimitedJSONDisallowUnknownFields(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	return decodeLimitedJSONWithOptions(w, r, limit, dst, true, false, "")
}

func decodeOptionalLimitedJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	return decodeLimitedJSONWithOptions(w, r, limit, dst, false, true, "")
}

func decodeLimitedJSONWithOptions(w http.ResponseWriter, r *http.Request, limit int64, dst any, disallowUnknown, allowEmpty bool, tooLargeMsg string) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if disallowUnknown {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		if allowEmpty && err == io.EOF {
			return true
		}
		writeJSONDecodeErrorWith(w, err, tooLargeMsg)
		return false
	}

	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			httpErr(w, http.StatusBadRequest, "bad json")
		} else {
			writeJSONDecodeErrorWith(w, err, tooLargeMsg)
		}
		return false
	}
	return true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	writeJSONDecodeErrorWith(w, err, "")
}

func writeJSONDecodeErrorWith(w http.ResponseWriter, err error, tooLargeMsg string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		if tooLargeMsg == "" {
			tooLargeMsg = "request body too large"
		}
		httpErr(w, http.StatusRequestEntityTooLarge, tooLargeMsg)
		return
	}
	httpErr(w, http.StatusBadRequest, "bad json")
}

func readLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	data, err := io.ReadAll(&io.LimitedReader{R: r.Body, N: limit + 1})
	if isBodyTooLarge(err) || int64(len(data)) > limit {
		httpErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return nil, false
	}
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad body")
		return nil, false
	}
	return data, true
}

func isBodyTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}
