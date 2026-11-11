package android

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// deviceShell runs argv the way `adb shell` delivers it: the arguments are
// joined with spaces and the device's shell parses the resulting line.
func deviceShell(t *testing.T, argv []string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	if len(argv) > 0 && argv[0] == "shell" {
		argv = argv[1:] // the adb subcommand, not part of the device command line
	}
	out, err := exec.Command("/bin/sh", "-c", strings.Join(argv, " ")).CombinedOutput()
	return string(out), err
}

// `adb shell sh -c <multi-line script>` only hands the first word to sh -c; the
// rest runs in the outer shell, so `set -e` never applied and a failed mount
// still reported success. The script must reach sh -c as one argument.
func TestShellScriptArgsRunWholeScriptUnderSetE(t *testing.T) {
	script := "set -e\nfalse\necho 'reached past the failure'"
	out, err := deviceShell(t, shellScriptArgs(script))
	if err == nil {
		t.Errorf("a failing step must fail the script, got success with output %q", out)
	}
	if strings.Contains(out, "reached") {
		t.Errorf("script continued after a failed step under set -e: %q", out)
	}
}

func TestShellScriptArgsPreserveQuotes(t *testing.T) {
	script := "X='a b'\nsh -c 'printf \"%s|\" \"$1\"' _ \"$X\"\nprintf '%s' \"it's\""
	out, err := deviceShell(t, shellScriptArgs(script))
	if err != nil || out != "a b|it's" {
		t.Fatalf("got %q, %v; want %q", out, err, "a b|it's")
	}
}

func TestInstallSystemCATmpfsSendsScriptAsOneShellWord(t *testing.T) {
	pemBytes := testCAPEM(t)
	var shellCalls [][]string
	stubADB(t, func(args ...string) ([]byte, error) {
		if len(args) > 3 && args[2] == "shell" && args[3] == "sh" {
			shellCalls = append(shellCalls, append([]string(nil), args[3:]...))
		}
		return nil, nil
	})
	if err := InstallSystemCATmpfs(Device{Serial: "dev", State: "device"}, pemBytes); err != nil {
		t.Fatal(err)
	}
	if len(shellCalls) != 1 {
		t.Fatalf("want one sh -c call, got %v", shellCalls)
	}
	// Compared structurally on purpose: the overlay script must never be run
	// on the machine executing the tests.
	name, err := systemCAFilename(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	want := shellScriptArgs(tmpfsOverlayScript(tmpfsStageDir + "-src/" + name))[1:]
	if len(shellCalls[0]) != len(want) || shellCalls[0][2] != want[2] {
		t.Fatalf("sh -c must receive the whole script as one quoted word, got %d args: %q", len(shellCalls[0]), shellCalls[0])
	}
	if !strings.HasPrefix(want[2], "'set -e\n") || !strings.HasSuffix(want[2], "\nfi'") {
		t.Fatalf("script is not a single quoted word: %q", want[2])
	}
}
