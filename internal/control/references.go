package control

import (
	_ "embed"
	"net/http"
)

//go:embed checks_reference.md
var checksReferenceMD []byte

//go:embed codecs_reference.md
var codecsReferenceMD []byte

func (h *checksAPI) checksReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"markdown": string(checksReferenceMD)})
}

func (h *checksAPI) codecsReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"markdown": string(codecsReferenceMD)})
}
