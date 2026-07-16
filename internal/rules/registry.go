package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// InstallRecord is one pack the operator has installed: its manifest identity
// plus the check ids it owns (so `rules remove` can delete exactly those files).
type InstallRecord struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Installed string   `json:"installed"`
	Source    string   `json:"source,omitempty"`
	IDs       []string `json:"ids"`
}

// Registry tracks installed packs in <root>/packs/registry.json. It is the
// source of truth for `rules list` / `rules remove` and for the REST/MCP pack
// surfaces. Filesystem layout for installed checks: passive → <root>/checks,
// active → <root>/active-checks (same dirs the engines already read), so an
// installed pack's checks run immediately with everything else.
type Registry struct {
	path string
}

// NewRegistry opens (or creates) the pack registry rooted at root (the global
// interseptor data dir, e.g. ~/.interseptor).
func NewRegistry(root string) *Registry {
	return &Registry{path: filepath.Join(root, "packs", "registry.json")}
}

type registryFile struct {
	Packs []InstallRecord `json:"packs"`
}

func (r *Registry) load() (registryFile, error) {
	var rf registryFile
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return rf, nil
		}
		return rf, err
	}
	if err := json.Unmarshal(data, &rf); err != nil {
		return rf, fmt.Errorf("rules: parse registry: %w", err)
	}
	return rf, nil
}

func (r *Registry) save(rf registryFile) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, data, 0o644)
}

// List returns installed packs, sorted by name.
func (r *Registry) List() ([]InstallRecord, error) {
	rf, err := r.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(rf.Packs, func(i, j int) bool { return rf.Packs[i].Name < rf.Packs[j].Name })
	return rf.Packs, nil
}

// Get returns one installed pack by name.
func (r *Registry) Get(name string) (InstallRecord, bool, error) {
	rf, err := r.load()
	if err != nil {
		return InstallRecord{}, false, err
	}
	for _, p := range rf.Packs {
		if p.Name == name {
			return p, true, nil
		}
	}
	return InstallRecord{}, false, nil
}

// Remove deletes a pack's check files and drops its registry entry. Files a
// user hand-edited are left in place (an entry only owns ids it recorded).
func (r *Registry) Remove(name, checksDir, activeChecksDir string) (int, error) {
	rf, err := r.load()
	if err != nil {
		return 0, err
	}
	idx := -1
	for i, p := range rf.Packs {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, fmt.Errorf("rules: pack %q is not installed", name)
	}
	rec := rf.Packs[idx]
	removed := 0
	for _, id := range rec.IDs {
		// remove from whichever dir holds it (passive or active); ignore "not found".
		for _, dir := range []string{checksDir, activeChecksDir} {
			p := filepath.Join(dir, id+".star")
			if err := os.Remove(p); err == nil {
				removed++
				break
			}
		}
	}
	rf.Packs = append(rf.Packs[:idx], rf.Packs[idx+1:]...)
	if err := r.save(rf); err != nil {
		return removed, err
	}
	return removed, nil
}

// record installs (or upgrades) a pack's files on disk and records it. Existing
// files with the same id are overwritten (an upgrade). Returns the count written.
func (r *Registry) record(m Manifest, files []File, checksDir, activeChecksDir, source string) (int, error) {
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(activeChecksDir, 0o755); err != nil {
		return 0, err
	}
	written := 0
	ids := make([]string, 0, len(files))
	for _, f := range files {
		dir := checksDir
		if f.Kind == KindActive {
			dir = activeChecksDir
		}
		if err := os.WriteFile(filepath.Join(dir, f.ID+".star"), f.Data, 0o644); err != nil {
			return written, err
		}
		ids = append(ids, f.ID)
		written++
	}
	rf, err := r.load()
	if err != nil {
		return written, err
	}
	rec := InstallRecord{Name: m.Name, Version: m.Version, IDs: ids, Source: source}
	// replace an existing entry for the same pack (upgrade), else append.
	out := rf.Packs[:0]
	replaced := false
	for _, p := range rf.Packs {
		if p.Name == m.Name {
			if !replaced {
				out = append(out, rec)
				replaced = true
			}
			continue
		}
		out = append(out, p)
	}
	if !replaced {
		out = append(out, rec)
	}
	rf.Packs = out
	return written, r.save(rf)
}

// InstallStream reads a pack from r, verifies it, writes its checks to disk, and
// records it in the registry. source is stored for provenance (e.g. a URL).
func (r *Registry) InstallStream(rdr readSeekFree, checksDir, activeChecksDir, source string) (Manifest, int, error) {
	m, files, err := ReadPack(rdr)
	if err != nil {
		return m, 0, err
	}
	n, err := r.record(m, files, checksDir, activeChecksDir, source)
	return m, n, err
}

// InstallFile installs a pack from a local .tar.gz path.
func (r *Registry) InstallFile(path, checksDir, activeChecksDir string) (Manifest, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, 0, err
	}
	defer f.Close()
	m, n, err := r.InstallStream(f, checksDir, activeChecksDir, path)
	return m, n, err
}

// readSeekFree is io.Reader, named to document that ReadPack only needs reading.
type readSeekFree = interface{ Read(p []byte) (n int, err error) }
