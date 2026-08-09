//go:build darwin

package proc_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Veyal/interseptor/internal/proc"
)

func TestLaunchdServerJobWithRunner(t *testing.T) {
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "list":
			return []byte("PID\tStatus\tLabel\n31777\t0\tcom.interseptor.ui\n19227\t-15\tcom.interseptor.vault\n"), nil
		case "print":
			return []byte("pid = 31777\narguments = {\n\t/Users/des/.local/bin/interseptor\n\t--project\n\tebranch-banksumut\n}\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}

	label, found, err := proc.LaunchdServerJobWithRunner(31777, run)
	if err != nil || !found || label != "com.interseptor.ui" {
		t.Fatalf("LaunchdServerJobWithRunner = %q, %v, %v", label, found, err)
	}
	want := [][]string{{"list"}, {"print", "gui/501/com.interseptor.ui"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestBootoutLaunchdServerWithRunnerSkipsVault(t *testing.T) {
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "list":
			return []byte("19227\t-15\tcom.interseptor.vault\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	label, found, err := proc.BootoutLaunchdServerWithRunner(19227, run)
	if err != nil || found || label != "" {
		t.Fatalf("BootoutLaunchdServerWithRunner = %q, %v, %v", label, found, err)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"list"}) {
		t.Fatalf("commands = %#v, want only launchctl list", calls)
	}
}

func TestBootoutLaunchdServerWithRunnerConstructsBootout(t *testing.T) {
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "list":
			return []byte("31777\t0\tcom.interseptor.ui\n"), nil
		case "print":
			return []byte("pid = 31777\narguments = {\n\t/Users/des/.local/bin/interseptor\n\t--project\n\texample\n}\n"), nil
		case "bootout":
			return nil, nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	label, found, err := proc.BootoutLaunchdServerWithRunner(31777, run)
	if err != nil || !found || label != "com.interseptor.ui" {
		t.Fatalf("BootoutLaunchdServerWithRunner = %q, %v, %v", label, found, err)
	}
	if !reflect.DeepEqual(calls[2], []string{"bootout", "gui/501/com.interseptor.ui"}) {
		t.Fatalf("bootout command = %#v", calls[2])
	}
}
