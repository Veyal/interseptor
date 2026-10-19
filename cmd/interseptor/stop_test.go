package main

import (
	"reflect"
	"testing"

	"github.com/Veyal/interseptor/internal/proc"
)

func TestRunStopBootsOutLaunchdJobBeforeSignaling(t *testing.T) {
	oldList, oldBootout := stopList, stopBootout
	oldGraceful, oldForce, oldAlive := stopGraceful, stopForce, stopAlive
	t.Cleanup(func() {
		stopList, stopBootout = oldList, oldBootout
		stopGraceful, stopForce, stopAlive = oldGraceful, oldForce, oldAlive
	})

	calls := []string{}
	stopList = func() ([]proc.Proc, error) {
		return []proc.Proc{{PID: 31777, Path: "/Users/example/.local/bin/interseptor", Role: proc.RoleServer}}, nil
	}
	stopBootout = func(pid int) (string, bool, error) {
		calls = append(calls, "bootout")
		if pid != 31777 {
			t.Fatalf("bootout PID = %d, want 31777", pid)
		}
		return "com.interseptor.ui", true, nil
	}
	stopGraceful = func(pid int) error {
		calls = append(calls, "graceful")
		if pid != 31777 {
			t.Fatalf("graceful PID = %d, want 31777", pid)
		}
		return nil
	}
	stopForce = func(pid int) error {
		calls = append(calls, "force")
		return nil
	}
	stopAlive = func(int) bool { return false }

	if err := runStop([]string{"--timeout", "0"}); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	if want := []string{"bootout", "graceful"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("stop calls = %v, want %v", calls, want)
	}
}

func TestRunStopDoesNotBootoutVault(t *testing.T) {
	oldList, oldBootout := stopList, stopBootout
	t.Cleanup(func() {
		stopList, stopBootout = oldList, oldBootout
	})

	bootoutCalled := false
	stopList = func() ([]proc.Proc, error) {
		return []proc.Proc{{PID: 19227, Path: "/Users/example/.local/bin/interseptor", Role: proc.RoleVault}}, nil
	}
	stopBootout = func(int) (string, bool, error) {
		bootoutCalled = true
		return "", false, nil
	}

	if err := runStop(nil); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	if bootoutCalled {
		t.Fatal("bootout called for vault process")
	}
}

// stopRecorder swaps the stop seams for fakes that record which PIDs were
// signalled, and reports every process as already exited.
func stopRecorder(t *testing.T, procs []proc.Proc) *[]int {
	t.Helper()
	oldList, oldBootout := stopList, stopBootout
	oldGraceful, oldForce, oldAlive := stopGraceful, stopForce, stopAlive
	t.Cleanup(func() {
		stopList, stopBootout = oldList, oldBootout
		stopGraceful, stopForce, stopAlive = oldGraceful, oldForce, oldAlive
	})
	var stopped []int
	stopList = func() ([]proc.Proc, error) { return procs, nil }
	stopBootout = func(int) (string, bool, error) { return "", false, nil }
	stopGraceful = func(pid int) error { stopped = append(stopped, pid); return nil }
	stopForce = func(int) error { return nil }
	stopAlive = func(int) bool { return false }
	return &stopped
}

var mixedProcs = []proc.Proc{
	{PID: 10, Role: proc.RoleServer},
	{PID: 11, Role: proc.RoleServer},
	{PID: 20, Role: proc.RoleMCP},
	{PID: 21, Role: proc.RoleMCP},
	{PID: 30, Role: proc.RoleVault},
	{PID: 40, Role: proc.RoleLauncher},
}

func TestRunStopStopsEveryServerButNotMCP(t *testing.T) {
	stopped := stopRecorder(t, mixedProcs)
	if err := runStop([]string{"--timeout", "0"}); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	if want := []int{10, 11}; !reflect.DeepEqual(*stopped, want) {
		t.Fatalf("stopped %v, want %v", *stopped, want)
	}
}

func TestRunStopAllStopsEverything(t *testing.T) {
	stopped := stopRecorder(t, mixedProcs)
	if err := runStop([]string{"--all", "--timeout", "0"}); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	if want := []int{10, 11, 20, 21, 30, 40}; !reflect.DeepEqual(*stopped, want) {
		t.Fatalf("stopped %v, want %v", *stopped, want)
	}
}

func TestRunStopUnknownRoles(t *testing.T) {
	// Windows cannot read arguments, so every process is RoleUnknown: a lone
	// one is stopped as before, several are left alone without --all because
	// they may be MCP bridges, vaults or launchers.
	stopped := stopRecorder(t, []proc.Proc{{PID: 50, Role: proc.RoleUnknown}})
	if err := runStop([]string{"--timeout", "0"}); err != nil || !reflect.DeepEqual(*stopped, []int{50}) {
		t.Fatalf("lone unknown: err=%v stopped=%v, want [50]", err, *stopped)
	}
	stopped = stopRecorder(t, []proc.Proc{{PID: 50, Role: proc.RoleUnknown}, {PID: 51, Role: proc.RoleUnknown}})
	if err := runStop([]string{"--timeout", "0"}); err != nil || len(*stopped) != 0 {
		t.Fatalf("several unknown: err=%v stopped=%v, want none", err, *stopped)
	}
	stopped = stopRecorder(t, []proc.Proc{{PID: 50, Role: proc.RoleUnknown}, {PID: 51, Role: proc.RoleUnknown}})
	if err := runStop([]string{"--all", "--timeout", "0"}); err != nil || !reflect.DeepEqual(*stopped, []int{50, 51}) {
		t.Fatalf("several unknown --all: err=%v stopped=%v, want [50 51]", err, *stopped)
	}
}
