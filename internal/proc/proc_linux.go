//go:build linux

package proc

import (
	"os"
	"strconv"
)

// List returns every running interceptor process (excluding the caller).
func List() ([]Proc, error) {
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return listViaPgrep(self)
	}

	var procs []Proc
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		if p, ok := procFromProcFS(pid); ok {
			procs = append(procs, p)
		}
	}
	return procs, nil
}
