package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Veyal/interseptor/internal/postman"
)

const maxPostmanImportBytes = 64 << 20

// importPostman prepares a Postman collection for Repeater. Unlike HAR/Burp
// imports, this endpoint does not create History flows because a collection is
// a set of request templates, not evidence that traffic was captured.
func (h *projectAPI) importPostman(w http.ResponseWriter, r *http.Request) {
	data, ok := readLimitedBody(w, r, maxPostmanImportBytes)
	if !ok {
		return
	}
	collection, environment, err := postmanImportDocuments(data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := postman.ParseWithEnvironment(collection, environment)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "not a usable Postman collection: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func postmanImportDocuments(data []byte) ([]byte, []byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("empty Postman import")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	collection, wrapped := top["collection"]
	if !wrapped {
		return trimmed, nil, nil
	}
	if len(bytes.TrimSpace(collection)) == 0 || bytes.Equal(bytes.TrimSpace(collection), []byte("null")) {
		return nil, nil, fmt.Errorf("Postman import wrapper is missing collection")
	}
	return collection, top["environment"], nil
}
