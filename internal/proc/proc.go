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
	Path string   // absolute path to the binary, if known
	Args []string // process arguments, when available
	Role Role
}

// Role identifies an Interseptor process mode.
type Role string

const (
	RoleServer   Role = "server"
	RoleVault    Role = "vault"
	RoleLauncher Role = "launcher"
	RoleUnknown  Role = "unknown"
)

// ClassifyRole identifies non-server modes from executable arguments.
func ClassifyRole(args []string) Role {
	for _, arg := range args[1:] {
		switch strings.TrimSpace(arg) {
		case "vault":
			return RoleVault
		case "launcher", "macapp":
			return RoleLauncher
		}
	}
	return RoleServer
}

// Stoppable reports whether default stop may target p.
func (p Proc) Stoppable() bool { return p.Role == RoleServer || p.Role == RoleUnknown }

func IsServerArgs(args []string) bool {
	if len(args) == 0 || !matchesInterseptor(baseFromPath(args[0])) {
		return false
	}
	return ClassifyRole(args) == RoleServer
}

func ParseLaunchdArguments(output string) []string {
	var args []string
	inArguments := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "arguments = {" {
			inArguments = true
			continue
		}
		if !inArguments {
			continue
		}
		if trimmed == "}" {
			break
		}
		if trimmed != "" {
			args = append(args, trimmed)
		}
	}
	return args
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
