package control

import (
	_ "embed"
	"net/http"
)

//go:embed codecs_reference.md
var codecsReferenceMD []byte

func (h *codecsAPI) codecsReference(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"markdown": string(codecsReferenceMD)})
}
