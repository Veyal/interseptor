package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const managedUIAuditEnv = "INTERSEPTOR_UI_AUDIT_MANAGED"

// projectSwitchAllowed disables re-exec only for the browser audit's owned,
// disposable candidate. Normal application launches preserve project switching.
func projectSwitchAllowed() bool {
	return os.Getenv(managedUIAuditEnv) != "1"
}

// resolveProjectDir maps a --project / INTERSEPTOR_PROJECT value to a display
// name and an absolute data directory. A bare token (no path separator) is a
// named project under projectsDir; anything that looks like a path (absolute,
// "~/…", or containing a separator) is used as a literal directory.
func resolveProjectDir(projectsDir, v, homeDir string) (name, dir string) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "~/") {
		v = filepath.Join(homeDir, v[2:])
	}
	// "." and ".." are directories, never names: as names they would resolve to
	// the projects folder itself (or the global root) and open a store there.
	if filepath.IsAbs(v) || strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
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
// bare names — and "default" — are stored by name; paths are stored by their
// resolved absolute directory.
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

// lastProjectPath records the most recently active name or absolute directory.
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
	// Publish through a same-directory temp file: a plain WriteFile truncates
	// first, and an instance starting in that window would read an empty name and
	// silently open "default". 0600 because the value can now be a client folder.
	path := lastProjectPath(globalDir)
	tmp, err := os.CreateTemp(globalDir, ".active-project-*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(name + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// rememberActiveProject persists paths by directory, not their ambiguous basename.
// What is stored is derived only from the project that was actually opened
// (name, dir), never from a second read of the active-project file: another
// instance may have rewritten that file since selectProject read it, and a
// path remembered a moment ago would then be saved back as its bare basename —
// the next launch would open (and create) projects/<basename> instead.
func rememberActiveProject(globalDir, _ /* projectArg */, name, dir string) {
	writeLastProject(globalDir, restartProjectTarget(globalDir, name, dir))
}

// resumeHint tells the operator how to get past a remembered external project
// that is gone. Without it a plain launch (and the desktop app, which passes no
// flags) fails on every start with no visible way out.
const resumeHint = " — reconnect that folder, or start with --project default (or --project <name|path>) to open another project"

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
		// Do not silently create an empty replacement for a missing external project.
		v = readLastProject(globalDir)
		if filepath.IsAbs(v) {
			if info, err := os.Stat(v); err != nil {
				return "", "", fmt.Errorf("resume project %q: %w%s", v, err, resumeHint)
			} else if !info.IsDir() {
				return "", "", fmt.Errorf("resume project %q: not a directory%s", v, resumeHint)
			}
		}
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
