//go:build darwin

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// List returns every running interseptor process (excluding the caller).
// macOS has no /proc — use pgrep directly.
func List() ([]Proc, error) {
	self := os.Getpid()
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return listViaPgrep(self)
	}
	var procs []Proc
	for _, line := range strings.Split(string(out), "\n") {
		pid, path, args, ok := parsePSLine(line)
		if !ok || pid == self {
			continue
		}
		procs = append(procs, Proc{PID: pid, Path: path, Args: args, Role: ClassifyRole(args)})
	}
	return procs, nil
}

// parsePSLine parses one "pid command..." line from ps. The command line is
// space-joined, so an executable under a path with spaces cannot be split on
// whitespace: the path is the shortest space-delimited prefix of the command
// whose base name is an Interseptor binary; the rest are its arguments.
func parsePSLine(line string) (pid int, path string, args []string, ok bool) {
	line = strings.TrimSpace(line)
	pidStr, cmd, found := strings.Cut(line, " ")
	if !found {
		return 0, "", nil, false
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, "", nil, false
	}
	cmd = strings.TrimSpace(cmd)
	for end := 0; end <= len(cmd); end++ {
		if end < len(cmd) && cmd[end] != ' ' {
			continue
		}
		if candidate := cmd[:end]; candidate != "" && matchesInterseptor(baseFromPath(candidate)) {
			return pid, candidate, append([]string{candidate}, strings.Fields(cmd[end:])...), true
		}
	}
	return 0, "", nil, false
}

// aliveInterseptor reports whether pid is alive AND its command name is an
// Interseptor binary, closing the same PID-reuse race that aliveInterseptor
// guards against on Windows/Linux. macOS has no /proc, but `ps -p <pid> -o
// comm=` is a cheap, single-process query — no need to fall back to the
// generic Alive(pid) here.
func aliveInterseptor(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	if comm == "" {
		return false
	}
	return matchesInterseptor(baseFromPath(comm))
}
