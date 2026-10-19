//go:build unix

package appwindow

import (
	"os"
	"os/exec"
	"syscall"
)

// detach puts the browser in its own process group, so a Ctrl+C aimed at the
// terminal running Interseptor does not also kill the window.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// ownedByCurrentUser reports whether fi belongs to the user running Interseptor.
func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
