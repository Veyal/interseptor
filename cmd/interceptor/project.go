package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// errQuit is returned by selectProject when the user chooses to quit at the
// startup project picker; run() treats it as a clean exit.
var errQuit = errors.New("quit")

// resolveProjectDir maps a --project / INTERCEPTOR_PROJECT value to a display
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

// selectProject decides which project directory to use. When flagOrEnv is set
// the choice is non-interactive. Otherwise, if interactive, it prompts on
// out/in (Burp-style: new project / continue saved / default); if not
// interactive it falls back to the default project, which is the global root
// itself so existing single-project installs keep working unchanged.
func selectProject(in io.Reader, out io.Writer, globalDir, flagOrEnv, homeDir string, interactive bool) (name, dir string, err error) {
	projectsDir := filepath.Join(globalDir, "projects")

	if strings.TrimSpace(flagOrEnv) != "" {
		name, dir = resolveProjectDir(projectsDir, flagOrEnv, homeDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		return name, dir, nil
	}

	if !interactive {
		return "default", globalDir, nil
	}

	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintln(out, "\nInterceptor — choose a project:")
		fmt.Fprintln(out, "  1) New project")
		fmt.Fprintln(out, "  2) Continue from a saved project")
		fmt.Fprintln(out, "  [Enter] Default project")
		fmt.Fprintln(out, "  q) Quit")
		fmt.Fprint(out, "> ")
		if !sc.Scan() {
			// EOF (e.g. closed stdin) → fall back to the default project.
			return "default", globalDir, nil
		}
		switch strings.TrimSpace(sc.Text()) {
		case "":
			return "default", globalDir, nil
		case "1":
			name, dir, err = promptNewProject(sc, out, projectsDir)
			if err != nil {
				fmt.Fprintf(out, "  %v\n", err)
				continue
			}
			return name, dir, nil
		case "2":
			name, dir, ok := promptContinueProject(sc, out, projectsDir)
			if !ok {
				continue
			}
			return name, dir, nil
		case "q", "Q":
			return "", "", errQuit
		default:
			fmt.Fprintln(out, "  Please enter 1, 2, q, or press Enter.")
		}
	}
}

func promptNewProject(sc *bufio.Scanner, out io.Writer, projectsDir string) (string, string, error) {
	fmt.Fprint(out, "Project name: ")
	if !sc.Scan() {
		return "", "", errQuit
	}
	name, err := sanitizeProjectName(sc.Text())
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(projectsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	return name, dir, nil
}

func promptContinueProject(sc *bufio.Scanner, out io.Writer, projectsDir string) (string, string, bool) {
	saved := listProjects(projectsDir)
	if len(saved) == 0 {
		fmt.Fprintln(out, "  No saved projects yet — choose 1 to create one.")
		return "", "", false
	}
	fmt.Fprintln(out, "  Saved projects:")
	for i, n := range saved {
		fmt.Fprintf(out, "    %d) %s\n", i+1, n)
	}
	fmt.Fprint(out, "Pick a number: ")
	if !sc.Scan() {
		return "", "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(sc.Text()))
	if err != nil || n < 1 || n > len(saved) {
		fmt.Fprintln(out, "  Invalid selection.")
		return "", "", false
	}
	name := saved[n-1]
	return name, filepath.Join(projectsDir, name), true
}

// isInteractive reports whether stdin is a terminal, so we only show the
// project picker for real interactive launches (never under pipes/CI/tests).
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
