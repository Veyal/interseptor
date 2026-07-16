package control

import (
	"io"
	"net/http"

	"github.com/Veyal/interseptor/internal/rules"
)

// Rule-pack management REST surface: list/info installed packs, install a pack
// from an uploaded .tar.gz (verified against its manifest's sha256s), and remove
// one. Mirrors the `interseptor rules` CLI; install/remove require full-scope
// (the guard blocks read-only keys from mutating methods) and broadcast
// checks.update so the Scanner UI refreshes the custom-checks list.
func (h *Hub) packsRegistry() *rules.Registry {
	return rules.NewRegistry(h.GlobalDir)
}

func (h *Hub) listPacks(w http.ResponseWriter, r *http.Request) {
	packs, err := h.packsRegistry().List()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": packs})
}

func (h *Hub) getPack(w http.ResponseWriter, r *http.Request) {
	rec, ok, err := h.packsRegistry().Get(r.PathValue("name"))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !ok {
		httpErr(w, http.StatusNotFound, "pack not installed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *Hub) installPack(w http.ResponseWriter, r *http.Request) {
	m, n, err := h.packsRegistry().InstallStream(
		io.LimitReader(r.Body, maxArchiveBytes),
		h.ChecksDir, h.ActiveChecksDir, "upload",
	)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "install failed: "+err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{
		"name": m.Name, "version": m.Version, "installed": n,
	})
}

func (h *Hub) removePack(w http.ResponseWriter, r *http.Request) {
	n, err := h.packsRegistry().Remove(r.PathValue("name"), h.ChecksDir, h.ActiveChecksDir)
	if err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{"removed": n})
}
