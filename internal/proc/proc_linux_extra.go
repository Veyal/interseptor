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

	args := readProcArgs(dir)
	role := ClassifyRole(args)
	if matchesInterseptor(comm) {
		return Proc{PID: pid, Path: exePath, Args: args, Role: role}, true
	}
	if exeBase != "" && matchesInterseptor(exeBase) {
		return Proc{PID: pid, Path: exePath, Args: args, Role: role}, true
	}
	return Proc{}, false
}

func readProcArgs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return parts
}
