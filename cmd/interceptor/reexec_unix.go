//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reexecProject replaces the current process image with a fresh Interceptor
// pointed at the named project. syscall.Exec keeps the same PID and atomically
// swaps the image, so the proxy/control listeners (opened close-on-exec) are
// released exactly as the new image rebinds them — no port-handoff race.
func reexecProject(exe, target string) error {
	return syscall.Exec(exe, []string{exe, "--project", target}, os.Environ())
}
