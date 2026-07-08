// Package proc discovers and stops running Interseptor processes by image name.
package proc

import (
	"path/filepath"
	"strings"
)

const (
	unixBinaryName    = "interseptor"
	windowsBinaryName = "interseptor.exe"
)

// Proc is a discovered interseptor process.
type Proc struct {
	PID  int
	Path string // absolute path to the binary, if known
}

// matchesInterseptor reports whether baseName is an Interseptor executable.
func matchesInterseptor(baseName string) bool {
	baseName = strings.TrimSpace(baseName)
	return baseName == unixBinaryName || baseName == windowsBinaryName
}

// baseFromPath returns the executable base name from path, or "" when empty.
func baseFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// AliveInterseptor reports whether pid is alive *and* is actually running an
// Interseptor binary (image name "interseptor"/"interseptor.exe"), not some
// unrelated process that has since reused a recycled PID. Callers that are
// about to signal/kill a PID they previously recorded (e.g. the launcher's
// stop/allocatePorts paths) should prefer this over the generic Alive(pid) —
// on a long-running system PIDs get reused, and a plain liveness check can't
// tell "our child is still alive" apart from "some other process now has
// this PID". Falls back to Alive(pid) on platforms/paths where a cheap,
// per-PID image-name check isn't available (see per-OS implementations).
func AliveInterseptor(pid int) bool {
	return aliveInterseptor(pid)
}
