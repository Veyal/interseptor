package main

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/version"
)

// restartProjectTarget is the --project value that reopens the running project
// after a self-update restart: the bare name for a named or default project,
// otherwise the literal project directory.
func restartProjectTarget(globalDir, name, dir string) string {
	dir = filepath.Clean(dir)
	if name == "default" && dir == filepath.Clean(globalDir) {
		return name
	}
	if isBareProjectName(name) && dir == filepath.Join(globalDir, "projects", name) {
		return name
	}
	return dir
}

// wireSelfUpdate enables the web UI's one-click update. The new binary is
// relaunched through the project-switch re-exec path (same project, flags and
// ports): syscall.Exec on Unix, spawn-and-exit on Windows after FinishStaged
// moves the staged .exe into place.
func wireSelfUpdate(hub *control.Hub, projectTarget string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	u := version.NewUpdater(exe, runtime.GOOS)
	version.CleanupReplacedBinary(u.Resolved) // the .old a Windows update parked
	hub.Updater = u
	if !projectSwitchAllowed() {
		return
	}
	hub.RestartForUpdate = func(s version.StagedUpdate) error {
		if err := version.FinishStaged(s); err != nil {
			return err
		}
		return reexecProject(s.Dest, projectTarget)
	}
}
