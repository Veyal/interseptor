//go:build darwin

package proc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type CommandRunner func(...string) ([]byte, error)

var launchctlOutput CommandRunner = func(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).Output()
}

func LaunchdServerJob(pid int) (string, bool, error) {
	return LaunchdServerJobWithRunner(pid, launchctlOutput)
}

func LaunchdServerJobWithRunner(pid int, run CommandRunner) (string, bool, error) {
	uid := strconv.Itoa(os.Getuid())
	list, err := run("list")
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(list), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] == "PID" {
			continue
		}
		jobPID, err := strconv.Atoi(fields[0])
		if err != nil || jobPID != pid {
			continue
		}
		label := fields[2]
		if label == "com.interseptor.vault" {
			continue
		}
		printed, err := run("print", "gui/"+uid+"/"+label)
		if err != nil {
			return "", false, err
		}
		if launchdPID(string(printed)) != pid || !IsServerArgs(ParseLaunchdArguments(string(printed))) {
			return "", false, nil
		}
		return label, true, nil
	}
	return "", false, nil
}

func BootoutLaunchdServer(pid int) (string, bool, error) {
	return BootoutLaunchdServerWithRunner(pid, launchctlOutput)
}

func BootoutLaunchdServerWithRunner(pid int, run CommandRunner) (string, bool, error) {
	label, found, err := LaunchdServerJobWithRunner(pid, run)
	if err != nil || !found {
		return label, found, err
	}
	uid := strconv.Itoa(os.Getuid())
	if _, err := run("bootout", "gui/"+uid+"/"+label); err != nil {
		return "", false, fmt.Errorf("bootout launchd job %s: %w", label, err)
	}
	return label, true, nil
}

func launchdPID(output string) int {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "pid" {
			pid, _ := strconv.Atoi(fields[2])
			return pid
		}
	}
	return 0
}
