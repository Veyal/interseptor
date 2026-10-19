//go:build !unix && !windows

package appwindow

import (
	"os"
	"os/exec"
)

func detach(*exec.Cmd) {}

func ownedByCurrentUser(os.FileInfo) bool { return true }
