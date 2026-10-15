//go:build windows

package pdfexport

import "os/exec"

// isolateProcessGroup is a no-op on Windows: a new process group would change
// how console signals reach the browser without giving us a way to kill the
// tree, and Chrome's child processes exit with their parent here.
func isolateProcessGroup(*exec.Cmd) {}

// terminateProcessGroup kills the browser. Its children are tied to it by the
// job object Chrome creates for itself, so they go with it.
func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
