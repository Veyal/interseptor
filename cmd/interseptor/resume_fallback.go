package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// resumeFallbackDefault is the only accepted --resume-fallback value: open
	// the default project when the remembered external folder is unavailable.
	resumeFallbackDefault = "default"
	// envResumeFallback is the environment form. The desktop app sets it (it
	// starts the server with no flags and has no terminal to print the resume
	// error to), and an older server binary simply ignores it.
	envResumeFallback = "INTERSEPTOR_RESUME_FALLBACK"
	// envResumeFellBack marks a process that started through the fallback. A
	// restart of that process (self-update) is launched with --project default
	// and must not record "default" over the folder that is still remembered.
	envResumeFellBack = "INTERSEPTOR_RESUME_FELL_BACK"
	// startupNoticePrefix introduces the one-line notice in the server log. The
	// desktop supervisor (cmd/interseptor-macapp) relays lines carrying it.
	startupNoticePrefix = "startup notice: "
)

// resolveResumeFallback validates the --resume-fallback flag (which wins) or
// its environment form. Empty means "report a missing remembered folder".
func resolveResumeFallback(flagValue, envValue string) (string, error) {
	v := strings.TrimSpace(flagValue)
	if v == "" {
		v = strings.TrimSpace(envValue)
	}
	switch {
	case v == "":
		return "", nil
	case strings.EqualFold(v, resumeFallbackDefault):
		return resumeFallbackDefault, nil
	}
	return "", fmt.Errorf("invalid resume fallback %q (only %q is supported)", v, resumeFallbackDefault)
}

// startupProject is the project a start opens, plus what the operator must be
// told when it is not the one that was remembered.
type startupProject struct {
	name, dir string
	// notice is non-empty when the remembered external folder was unavailable
	// and the default project was opened in its place.
	notice string
	// keepRemembered leaves the active-project file alone, so reconnecting the
	// folder and starting again resumes it.
	keepRemembered bool
}

// remember records the opened project for the next plain start, unless this
// start is only standing in for a remembered folder that is unavailable.
func (p startupProject) remember(globalDir, projectArg string) {
	if p.keepRemembered {
		return
	}
	rememberActiveProject(globalDir, projectArg, p.name, p.dir)
}

// unavailableRememberedProject returns the remembered external folder and why
// it cannot be opened, or "" when there is none or it is a usable directory.
func unavailableRememberedProject(globalDir string) (path, reason string) {
	v := readLastProject(globalDir)
	if !filepath.IsAbs(v) {
		return "", ""
	}
	info, err := os.Stat(v)
	switch {
	case err != nil:
		return v, err.Error()
	case !info.IsDir():
		return v, "not a directory"
	}
	return "", ""
}

func resumeFallbackNotice(path, reason string) string {
	return fmt.Sprintf("The project folder %s is not available (%s), so the default project was opened instead. "+
		"The folder is still remembered: reconnect it and restart Interseptor to resume it.", path, reason)
}

// selectStartupProject is selectProject plus the resume fallback. With
// fallback == "" (every plain CLI start) it is exactly selectProject: a missing
// remembered folder is an error. With resumeFallbackDefault that one case opens
// the default project instead, creating nothing and leaving the remembered
// folder recorded.
func selectStartupProject(globalDir, flagOrEnv, homeDir, fallback string) (startupProject, error) {
	if fallback == resumeFallbackDefault && strings.TrimSpace(flagOrEnv) == "" {
		if path, reason := unavailableRememberedProject(globalDir); path != "" {
			return startupProject{name: "default", dir: globalDir, notice: resumeFallbackNotice(path, reason), keepRemembered: true}, nil
		}
	}
	name, dir, err := selectProject(globalDir, flagOrEnv, homeDir)
	if err != nil {
		return startupProject{}, err
	}
	return startupProject{name: name, dir: dir}, nil
}

// carryResumeFallback keeps a fallback start's state across a restart of the
// same process. fellBack is the inherited envResumeFellBack marker. The restart
// names the project explicitly (--project default), so without this it would
// record "default" over the folder the fallback deliberately left remembered.
func carryResumeFallback(p startupProject, globalDir string, fellBack bool) startupProject {
	if p.keepRemembered || !fellBack || p.name != "default" || filepath.Clean(p.dir) != filepath.Clean(globalDir) {
		return p
	}
	if path, reason := unavailableRememberedProject(globalDir); path != "" {
		p.keepRemembered = true
		p.notice = resumeFallbackNotice(path, reason)
	}
	return p
}
