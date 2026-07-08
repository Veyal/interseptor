//go:build linux

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func procFromProcFS(pid int) (Proc, bool) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))

	commBytes, err := os.ReadFile(filepath.Join(dir, "comm"))
	if err != nil {
		return Proc{}, false
	}
	comm := strings.TrimSpace(string(commBytes))

	exePath, _ := os.Readlink(filepath.Join(dir, "exe"))
	exeBase := baseFromPath(exePath)

	if matchesInterseptor(comm) {
		return Proc{PID: pid, Path: exePath}, true
	}
	if exeBase != "" && matchesInterseptor(exeBase) {
		return Proc{PID: pid, Path: exePath}, true
	}
	return Proc{}, false
}
