//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachInstance starts a project instance in its own session. The terminal
// delivers Ctrl-C to its foreground process group and SIGHUP to its session
// when it closes; a child left in the launcher's group and session received
// both and shut down with the launcher, although the launcher promises to leave
// its instances running.
func detachInstance(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
