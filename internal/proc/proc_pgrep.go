//go:build !windows

package proc

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func listViaPgrep(self int) ([]Proc, error) {
	out, err := exec.Command("pgrep", "-x", unixBinaryName).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("pgrep: %w", err)
	}

	var procs []Proc
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid == self {
			continue
		}
		if p, ok := procFromProcFS(pid); ok {
			procs = append(procs, p)
		} else {
			procs = append(procs, Proc{PID: pid})
		}
	}
	return procs, nil
}
