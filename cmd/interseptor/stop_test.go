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
		return []proc.Proc{{PID: 31777, Path: "/Users/des/.local/bin/interseptor", Role: proc.RoleServer}}, nil
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
		return []proc.Proc{{PID: 19227, Path: "/Users/des/.local/bin/interseptor", Role: proc.RoleVault}}, nil
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
