//go:build windows

package appwindow

import (
	"os"
	"os/exec"
	"syscall"
)

// detach starts the browser in a new process group, so a Ctrl+C aimed at the
// console running Interseptor does not also kill the window.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// ownedByCurrentUser is not checked on Windows: the profile lives under the
// user's own local app data, which other users cannot write.
func ownedByCurrentUser(os.FileInfo) bool { return true }
