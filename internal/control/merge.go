package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// Project merge = additive union of a peer's flows + findings into the active
// project (the "pull/push" of a git-like collaboration). Three entry points:
//   - mergeFile: receive an uploaded full-project archive and merge it (also the
//     push RECEIVER — a peer POSTs their archive here);
//   - mergePull: download a peer's archive over their tunnel (URL + key) and merge;
//   - mergePush: build our archive and POST it to a peer's /api/merge/file.
// Pull/push run server-side so the peer key never touches the operator's browser.

// mergeArchiveFile unpacks a full-project archive to a scratch dir and merges it
// into the active project, returning the merge stats. Shared by mergeFile/mergePull.
func (h *Hub) mergeArchiveFile(zipPath, label string) (store.MergeStats, error) {
	scratch, err := os.MkdirTemp("", "interseptor-merge-*")
	if err != nil {
		return store.MergeStats{}, err
	}
	defer os.RemoveAll(scratch)
	// Reuse the export unpacker: zip-slip guarded, member-allowlisted, requires the DB.
	if err := unpackFullArchive(zipPath, scratch); err != nil {
		return store.MergeStats{}, err
	}
	peerDB := filepath.Join(scratch, archiveDBName)
	peerBodies := filepath.Join(scratch, archiveBodyRoot)
	if _, err := os.Stat(peerBodies); err != nil {
		peerBodies = "" // archive had no bodies
	}
	return h.st.MergeFrom(peerDB, peerBodies, label)
}

// mergeFile is the push RECEIVER: it ingests an uploaded project archive and
// merges it into the active project. Requires a full-scope key (guarded).
func (h *Hub) mergeFile(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	tmp, err := os.CreateTemp("", "interseptor-merge-up-*.zip")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, io.LimitReader(r.Body, maxArchiveBytes)); err != nil {
		tmp.Close()
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmp.Close()
	stats, err := h.mergeArchiveFile(tmpPath, label)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "merge failed: "+err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "flow.new"})
	h.broadcast(map[string]any{"type": "findings.update"})
	writeJSON(w, http.StatusOK, stats)
}

// mergePull downloads a peer's full-project archive over their tunnel URL (with
// their access key) and merges it into the active project.
func (h *Hub) mergePull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PeerURL string `json:"peerUrl"`
		Key     string `json:"key"`
		Label   string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(in.PeerURL), "/")
	if base == "" || in.Key == "" {
		httpErr(w, http.StatusBadRequest, "peerUrl and key are required")
		return
	}
	tmpPath, err := downloadPeerArchive(base+"/api/export/full", in.Key)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "pull failed: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)
	label := in.Label
	if label == "" {
		label = hostLabel(base)
	}
	stats, err := h.mergeArchiveFile(tmpPath, label)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "merge failed: "+err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "flow.new"})
	h.broadcast(map[string]any{"type": "findings.update"})
	writeJSON(w, http.StatusOK, stats)
}

// mergePush builds an archive of the active project and POSTs it to a peer's
// /api/merge/file endpoint (with the peer's full-scope key).
func (h *Hub) mergePush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PeerURL string `json:"peerUrl"`
		Key     string `json:"key"`
		Label   string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(in.PeerURL), "/")
	if base == "" || in.Key == "" {
		httpErr(w, http.StatusBadRequest, "peerUrl and key are required")
		return
	}
	// Build a snapshot archive to a temp file.
	snap, err := h.snapshotDB()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "snapshot: "+err.Error())
		return
	}
	defer os.Remove(snap)
	arc, err := os.CreateTemp("", "interseptor-push-*.zip")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	arcPath := arc.Name()
	defer os.Remove(arcPath)
	if err := buildFullArchive(arc, snap, h.st.BodiesDir()); err != nil {
		arc.Close()
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	arc.Close()

	label := in.Label
	if label == "" && h.ProjectName != "" {
		label = h.ProjectName
	}
	stats, err := uploadPeerArchive(base+"/api/merge/file?label="+label, in.Key, arcPath)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "push failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// peerHTTPClient is the client used for server-side pull/push. A generous timeout
// covers large archive transfers.
var peerHTTPClient = &http.Client{Timeout: 10 * time.Minute}

// downloadPeerArchive GETs a peer's project export to a temp file, returning its path.
func downloadPeerArchive(url, key string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := peerHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peer returned %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "interseptor-pull-*.zip")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, maxArchiveBytes)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// uploadPeerArchive POSTs a project archive to a peer's merge endpoint and returns
// the peer's decoded MergeStats.
func uploadPeerArchive(url, key, archivePath string) (store.MergeStats, error) {
	var stats store.MergeStats
	f, err := os.Open(archivePath)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodPost, url, f)
	if err != nil {
		return stats, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := peerHTTPClient.Do(req)
	if err != nil {
		return stats, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return stats, fmt.Errorf("peer returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return stats, err
	}
	return stats, nil
}

// hostLabel derives a short provenance label from a peer base URL.
func hostLabel(base string) string {
	s := base
	for _, pfx := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, pfx)
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	if s == "" {
		return "peer"
	}
	return s
}
