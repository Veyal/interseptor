package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// externalProject is a project whose data directory lives outside
// GlobalDir/projects — e.g. a client engagement folder the operator chose
// explicitly. Unlike named projects (auto-discovered by listing
// GlobalDir/projects), these must be remembered so they reappear in the
// switcher instead of requiring the full path to be retyped every time.
type externalProject struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

const maxExternalProjects = 50

func externalProjectsPath(globalDir string) string {
	return filepath.Join(globalDir, "external-projects.json")
}

// readExternalProjects returns the remembered external projects, most
// recently used first. A missing or corrupt file yields an empty list.
func readExternalProjects(globalDir string) []externalProject {
	if globalDir == "" {
		return nil
	}
	b, err := os.ReadFile(externalProjectsPath(globalDir))
	if err != nil {
		return nil
	}
	var out []externalProject
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// rememberExternalProject records path as the most-recently-used external
// project (moving it to the front if already known), capped at
// maxExternalProjects entries so the file can't grow unbounded.
func rememberExternalProject(globalDir, name, path string) {
	if globalDir == "" {
		return
	}
	existing := readExternalProjects(globalDir)
	out := []externalProject{{Name: name, Path: path}}
	for _, e := range existing {
		if strings.EqualFold(e.Path, path) {
			continue
		}
		out = append(out, e)
	}
	if len(out) > maxExternalProjects {
		out = out[:maxExternalProjects]
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(globalDir, 0o755)
	_ = os.WriteFile(externalProjectsPath(globalDir), b, 0o644)
}

// isSafeExternalPath reports whether abs (already made absolute) is a
// reasonable project directory: not empty, not a filesystem/drive root (e.g.
// "/", "C:\", or the drive-relative "C:"), so a mistyped path can't point
// Interseptor's data — and the next re-exec's MkdirAll — at the top of a drive.
func isSafeExternalPath(abs string) bool {
	if abs == "" {
		return false
	}
	clean := filepath.Clean(abs)
	rest := strings.TrimPrefix(clean, filepath.VolumeName(clean))
	rest = strings.Trim(rest, `/\`)
	return rest != "" && rest != "."
}
