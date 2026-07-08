//go:build windows

package main

import (
	"os"
	"os/exec"
	"time"
)

// reexecProject starts a fresh Interseptor on the named project and then exits
// this process. Windows has no syscall.Exec — it returns "not supported by
// windows" — so we cannot swap the image in place; instead we spawn a child and
// quit so it can take over the proxy/control listeners. INTERSEPTOR_REEXEC tells
// the child to retry binding those ports while this process releases them (see
// listenRetry). The brief delay lets the HTTP "switching" response flush before
// we exit, and gives the child time to start.
func reexecProject(exe, target string) error {
	cmd := exec.Command(exe, "--project", target)
	cmd.Env = append(os.Environ(), "INTERSEPTOR_REEXEC=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}
