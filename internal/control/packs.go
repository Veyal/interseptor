package control

import (
	"bytes"
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
	allowUnsigned := r.URL.Query().Get("allowUnsigned") == "1" || r.URL.Query().Get("allowUnsigned") == "true"
	m, n, err := h.packsRegistry().InstallStreamOpts(
		io.LimitReader(r.Body, maxArchiveBytes),
		h.ChecksDir, h.ActiveChecksDir, "upload",
		rules.InstallOpts{AllowUnsigned: allowUnsigned},
	)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "install failed: "+err.Error())
		return
	}
	rec, _, _ := h.packsRegistry().Get(m.Name)
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{
		"name": m.Name, "version": m.Version, "installed": n, "signed": rec.Signed,
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

func (h *Hub) listPackCatalog(w http.ResponseWriter, r *http.Request) {
	packs, err := rules.ListCatalog()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	installed, _ := h.packsRegistry().List()
	have := map[string]string{}
	for _, p := range installed {
		have[p.Name] = p.Version
	}
	out := make([]map[string]any, 0, len(packs))
	for _, p := range packs {
		row := map[string]any{
			"name": p.Name, "version": p.Version, "description": p.Description,
			"author": p.Author, "checks": p.Checks,
		}
		if v, ok := have[p.Name]; ok {
			row["installed"] = true
			row["installedVersion"] = v
		} else {
			row["installed"] = false
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": out})
}

func (h *Hub) installCatalogPack(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var buf bytes.Buffer
	m, err := rules.BuildCatalogPack(name, &buf)
	if err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	got, n, err := h.packsRegistry().InstallStreamOpts(bytes.NewReader(buf.Bytes()), h.ChecksDir, h.ActiveChecksDir, "catalog",
		rules.InstallOpts{TrustBuiltin: true})
	if err != nil {
		httpErr(w, http.StatusBadRequest, "install failed: "+err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{
		"name": got.Name, "version": got.Version, "installed": n, "catalog": m.Name, "signed": "builtin",
	})
}
