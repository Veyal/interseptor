//go:build !windows

package proc

import "syscall"

// Graceful sends SIGTERM to pid.
func Graceful(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// Force sends SIGKILL to pid.
func Force(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}

// Alive reports whether pid still exists.
func Alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
