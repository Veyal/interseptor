//go:build !windows

package pdfexport

import (
	"os/exec"
	"syscall"
)

// isolateProcessGroup puts the browser in its own process group so the whole
// tree it spawns — renderer, GPU process, network service — can be terminated
// together. Chrome does not exit on its own after --print-to-pdf, so leaving
// those children behind would leak a browser per export.
func isolateProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateProcessGroup kills the browser's process group. SIGKILL is correct
// here: the PDF has already been read, and Chrome's own shutdown is exactly the
// thing that does not happen.
func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid != syscall.Getpgrp() {
		if syscall.Kill(-pgid, syscall.SIGKILL) == nil {
			return
		}
	}
	_ = cmd.Process.Kill()
}
