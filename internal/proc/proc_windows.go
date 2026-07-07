//go:build windows

package proc

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// List returns every running interceptor process (excluding the caller).
func List() ([]Proc, error) {
	self := os.Getpid()
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, fmt.Errorf("tasklist: %w", err)
	}

	var procs []Proc
	reader := csv.NewReader(bytes.NewReader(out))
	for {
		row, err := reader.Read()
		if err != nil {
			break
		}
		if len(row) < 2 {
			continue
		}
		image := strings.Trim(row[0], `"`)
		if !matchesInterceptor(image) {
			continue
		}
		pid, err := strconv.Atoi(strings.Trim(row[1], `"`))
		if err != nil || pid == self {
			continue
		}
		procs = append(procs, Proc{PID: pid, Path: image})
	}
	return procs, nil
}

// Graceful closes pid and its child tree without /F.
func Graceful(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T").Run()
}

// Force force-terminates pid and its child tree.
func Force(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F", "/T").Run()
}

// Alive reports whether pid still exists.
func Alive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(out))
	if s == "" || strings.HasPrefix(s, "INFO:") {
		return false
	}
	return strings.Contains(s, strconv.Itoa(pid))
}

// AliveInterceptor reports whether pid both exists AND names an Interceptor
// executable, matching List()'s image-name filter. Unlike Alive (a generic
// "does this PID exist" check relied on elsewhere for non-Interceptor PIDs),
// this guards specifically against PID reuse: if a spawned interceptor.exe
// child has already exited and the OS recycles its PID onto an unrelated
// process before the launcher notices, AliveInterceptor reports false rather
// than mistaking the new process for the old one — so a caller about to
// taskkill /F /T a registry PID can reconfirm it's still really an
// Interceptor process first.
func AliveInterceptor(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(out))
	if s == "" || strings.HasPrefix(s, "INFO:") {
		return false
	}

	reader := csv.NewReader(strings.NewReader(s))
	row, err := reader.Read()
	if err != nil || len(row) < 2 {
		return false
	}
	image := strings.Trim(row[0], `"`)
	rowPID := strings.Trim(row[1], `"`)
	if rowPID != strconv.Itoa(pid) {
		return false
	}
	return matchesInterceptor(image)
}
