//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// Ctrl-C in the launcher's terminal is delivered to the terminal's foreground
// process group, and closing it sends SIGHUP to the session. A project instance
// must be in neither, or "closing the launcher does not stop the instances it
// spawned" (docs/cli-reference.md) is false.
func TestLauncherInstanceRunsInItsOwnSession(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	lh := &launcherServer{exe: sleep}
	cmd := lh.instanceCommand("30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == syscall.Getpgrp() {
		t.Errorf("instance shares the launcher's process group %d: a terminal Ctrl-C reaches it", pgid)
	}
	if pgid != cmd.Process.Pid {
		t.Errorf("instance pgid = %d, want its own pid %d", pgid, cmd.Process.Pid)
	}
	// The instance's stdin must not be the launcher's terminal.
	if cmd.Stdin != nil && cmd.Stdin == os.Stdin {
		t.Error("instance inherits the launcher's stdin")
	}
}
