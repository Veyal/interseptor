//go:build darwin

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
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

// statPath is os.Stat, injectable so tests can fake executables on disk.
var statPath = os.Stat

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
		if candidate := cmd[:end]; isInterseptorExecutable(candidate) {
			return pid, candidate, append([]string{candidate}, strings.Fields(cmd[end:])...), true
		}
	}
	return 0, "", nil, false
}

// isInterseptorExecutable reports whether candidate (a space-delimited prefix
// of a ps command line) names an Interseptor binary. A prefix without spaces
// is the real argv[0]. A prefix containing spaces is ambiguous — it may be
// "vim /path/interseptor" — so it is accepted only when it is an absolute
// path to an existing executable regular file.
func isInterseptorExecutable(candidate string) bool {
	if candidate == "" || !matchesInterseptor(baseFromPath(candidate)) {
		return false
	}
	if !strings.Contains(candidate, " ") {
		return true
	}
	if !filepath.IsAbs(candidate) {
		return false
	}
	info, err := statPath(candidate)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
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
