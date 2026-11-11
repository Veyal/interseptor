//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachInstance starts a project instance in a new process group, so a Ctrl+C
// aimed at the console running the launcher does not also stop the instance.
func detachInstance(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
