package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// resolveProjectDir maps a --project / INTERSEPTOR_PROJECT value to a display
// name and an absolute data directory. A bare token (no path separator) is a
// named project under projectsDir; anything that looks like a path (absolute,
// "~/…", or containing a separator) is used as a literal directory.
func resolveProjectDir(projectsDir, v, homeDir string) (name, dir string) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "~/") {
		v = filepath.Join(homeDir, v[2:])
	}
	if filepath.IsAbs(v) || strings.ContainsAny(v, `/\`) {
		abs, err := filepath.Abs(v)
		if err != nil {
			abs = v
		}
		return filepath.Base(abs), abs
	}
	return v, filepath.Join(projectsDir, v)
}

// sanitizeProjectName validates a name typed at the "new project" prompt. It
// rejects empty names, "."/".." and anything with a path separator so a project
// name can never escape the projects directory.
func sanitizeProjectName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "." || v == ".." || strings.ContainsAny(v, `/\`) {
		return "", fmt.Errorf("invalid project name %q", v)
	}
	return v, nil
}

// isBareProjectName reports whether v is a plain, switchable project name (no
// path separators, "~", or leading "-") rather than a filesystem path. Only
// bare names — and "default" — are remembered as the active project, so a later
// plain launch can resume them; explicit --project /paths are treated as
// one-off and never overwrite the remembered name.
func isBareProjectName(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "." && v != ".." &&
		!strings.ContainsAny(v, `/\`) && !strings.HasPrefix(v, "~") && !strings.HasPrefix(v, "-")
}

// listProjects returns the names of saved projects (immediate subdirectories of
// projectsDir), sorted. A missing directory yields an empty list, not an error.
func listProjects(projectsDir string) []string {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// lastProjectPath is the file under globalDir that records the most recently
// active project name, so a plain (no-flag) launch can resume it.
func lastProjectPath(globalDir string) string {
	return filepath.Join(globalDir, "active-project")
}

// readLastProject returns the remembered active-project name, or "" if none.
func readLastProject(globalDir string) string {
	b, err := os.ReadFile(lastProjectPath(globalDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeLastProject best-effort records name as the active project so the next
// plain launch resumes it. Project selection lives entirely in the web UI now
// (no terminal prompt), so this is what makes a UI switch survive a restart.
func writeLastProject(globalDir, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	_ = os.MkdirAll(globalDir, 0o755)
	_ = os.WriteFile(lastProjectPath(globalDir), []byte(name+"\n"), 0o644)
}

// selectProject decides which project directory to use, with no terminal
// interaction — selecting, creating, and switching projects all happen in the
// web UI. Precedence: an explicit --project/INTERSEPTOR_PROJECT value wins;
// otherwise the most recently active project (remembered across restarts) is
// resumed; failing that, the "default" project, which is the global root itself
// so existing single-project installs keep working unchanged.
func selectProject(globalDir, flagOrEnv, homeDir string) (name, dir string, err error) {
	projectsDir := filepath.Join(globalDir, "projects")

	v := strings.TrimSpace(flagOrEnv)
	if v == "" {
		// No explicit project: resume whatever the UI last switched to.
		v = readLastProject(globalDir)
	}
	// "default" always means the global root, matching the no-flag default — so
	// switching to "default" returns to the original project, not a separate
	// projects/default.
	if v == "" || strings.EqualFold(v, "default") {
		return "default", globalDir, nil
	}

	name, dir = resolveProjectDir(projectsDir, v, homeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	return name, dir, nil
}
