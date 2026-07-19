package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/vault"
)

// Machine-wide vault client config (shared across projects on this host).
type vaultClientConfig struct {
	URL string `json:"url"`
	Key string `json:"key"`
}

func (h *Hub) vaultClientPath() string {
	if h.GlobalDir == "" {
		return ""
	}
	return filepath.Join(h.GlobalDir, "vault-client.json")
}

func (h *Hub) loadVaultClient() vaultClientConfig {
	p := h.vaultClientPath()
	if p == "" {
		return vaultClientConfig{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return vaultClientConfig{}
	}
	var c vaultClientConfig
	_ = json.Unmarshal(b, &c)
	return c
}

func (h *Hub) saveVaultClient(c vaultClientConfig) error {
	p := h.vaultClientPath()
	if p == "" {
		return fmt.Errorf("global data directory not configured")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func (h *Hub) getVaultConfig(w http.ResponseWriter, r *http.Request) {
	c := h.loadVaultClient()
	writeJSON(w, http.StatusOK, map[string]any{
		"url":    c.URL,
		"hasKey": c.Key != "",
		// never echo the full key back
	})
}

func (h *Hub) putVaultConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL *string `json:"url"`
		Key *string `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	c := h.loadVaultClient()
	if in.URL != nil {
		c.URL = strings.TrimRight(strings.TrimSpace(*in.URL), "/")
	}
	if in.Key != nil && strings.TrimSpace(*in.Key) != "" {
		c.Key = strings.TrimSpace(*in.Key)
	}
	if err := h.saveVaultClient(c); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": c.URL, "hasKey": c.Key != "", "saved": true})
}

func (h *Hub) vaultCreds() (base, key string, err error) {
	c := h.loadVaultClient()
	base = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	key = strings.TrimSpace(c.Key)
	if base == "" || key == "" {
		return "", "", fmt.Errorf("configure vault URL and key first (Settings → API → Share → Project Vault)")
	}
	return base, key, nil
}

func (h *Hub) vaultList(w http.ResponseWriter, r *http.Request) {
	base, key, err := h.vaultCreds()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/vault/projects", nil)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := peerHTTPClient.Do(req)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "vault unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		httpErr(w, http.StatusBadGateway, fmt.Sprintf("vault returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Hub) vaultBackup(w http.ResponseWriter, r *http.Request) {
	base, key, err := h.vaultCreds()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var in struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = h.ProjectName
	}
	if id == "" {
		id = "default"
	}
	if !vault.ValidSlug(id) {
		httpErr(w, http.StatusBadRequest, "invalid project id (letters, digits, - or _)")
		return
	}
	snap, err := h.snapshotDB()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	defer os.Remove(snap)
	arc, err := os.CreateTemp("", "interseptor-vault-*.zip")
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	arcPath := arc.Name()
	defer os.Remove(arcPath)
	if err := buildFullArchive(arc, snap, h.st.BodiesDir()); err != nil {
		arc.Close()
		httpInternalErr(w, err)
		return
	}
	if err := arc.Close(); err != nil {
		httpInternalErr(w, err)
		return
	}

	f, err := os.Open(arcPath)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	u := base + "/api/vault/projects/" + url.PathEscape(id)
	if in.Label != "" {
		u += "?label=" + url.QueryEscape(in.Label)
	}
	req, err := http.NewRequest(http.MethodPut, u, f)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ContentLength = fi.Size()
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/zip")
	if h.ProjectName != "" {
		req.Header.Set("X-Interseptor-Source-Host", h.ProjectName)
	}
	resp, err := peerHTTPClient.Do(req)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "vault unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		httpErr(w, http.StatusBadGateway, fmt.Sprintf("vault returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Hub) vaultImport(w http.ResponseWriter, r *http.Request) {
	base, key, err := h.vaultCreds()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var in struct {
		ID        string `json:"id"`
		Rev       int    `json:"rev"`
		Name      string `json:"name"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !vault.ValidSlug(in.ID) {
		httpErr(w, http.StatusBadRequest, "id required")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = in.ID
	}
	destDir, err := h.projectImportDir(name)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dirHasProject(destDir) && !in.Overwrite {
		httpErr(w, http.StatusConflict, "a project with that name already exists (pass overwrite=true to replace)")
		return
	}
	dl := base + "/api/vault/projects/" + url.PathEscape(in.ID) + "/latest"
	if in.Rev > 0 {
		dl = fmt.Sprintf("%s/api/vault/projects/%s/revs/%d", base, url.PathEscape(in.ID), in.Rev)
	}
	tmpPath, err := downloadPeerArchive(dl, key)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)
	if dirHasProject(destDir) && in.Overwrite {
		_ = os.RemoveAll(destDir)
	}
	if err := unpackFullArchive(tmpPath, destDir); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": true, "name": name, "dir": destDir})
}

func (h *Hub) vaultMerge(w http.ResponseWriter, r *http.Request) {
	base, key, err := h.vaultCreds()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var in struct {
		ID     string `json:"id"`
		Rev    int    `json:"rev"`
		Label  string `json:"label"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !vault.ValidSlug(in.ID) {
		httpErr(w, http.StatusBadRequest, "id required")
		return
	}
	dl := base + "/api/vault/projects/" + url.PathEscape(in.ID) + "/latest"
	if in.Rev > 0 {
		dl = fmt.Sprintf("%s/api/vault/projects/%s/revs/%d", base, url.PathEscape(in.ID), in.Rev)
	}
	tmpPath, err := downloadPeerArchive(dl, key)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)
	label := in.Label
	if label == "" {
		label = "vault/" + in.ID
	}
	if in.DryRun {
		stats, err := h.mergeArchivePreview(tmpPath, label)
		if err != nil {
			httpErr(w, http.StatusBadRequest, "preview failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dryRun": true, "flowsAdded": stats.FlowsAdded, "flowsSkipped": stats.FlowsSkipped,
			"findingsAdded": stats.FindingsAdded, "findingsSkipped": stats.FindingsSkipped,
			"bodiesAdded": stats.BodiesAdded,
		})
		return
	}
	stats, err := h.mergeArchiveFile(tmpPath, label)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "merge failed: "+err.Error())
		return
	}
	h.recordMergePresence("vault", base, label)
	h.broadcast(map[string]any{"type": "flow.new"})
	h.broadcast(map[string]any{"type": "findings.update"})
	writeJSON(w, http.StatusOK, stats)
}
