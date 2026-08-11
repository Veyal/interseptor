package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckValidateDetectsCompileErrors(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.star")
	os.WriteFile(good, []byte("def check(flow):\n    return []\n"), 0o644)
	bad := filepath.Join(dir, "bad.star")
	os.WriteFile(bad, []byte("def check(flow\n    return []"), 0o644)

	if err := checkValidate([]string{good}, false); err != nil {
		t.Fatalf("good file should validate clean: %v", err)
	}
	if err := checkValidate([]string{bad}, false); err == nil {
		t.Fatal("malformed file should fail validation")
	}
}

func TestCheckValidateActiveUsesActiveEngine(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ok.star")
	os.WriteFile(f, []byte("def check(point, baseline, probe):\n    return []\n"), 0o644)
	if err := checkValidate([]string{f}, true); err != nil {
		t.Fatalf("active check should validate clean under --active: %v", err)
	}
}

func TestCheckNewRefusesOverwrite(t *testing.T) {
	t.Setenv("INTERSEPTOR_DATA_DIR", t.TempDir())
	if err := checkNew([]string{"mycheck"}, false); err != nil {
		t.Fatalf("first new: %v", err)
	}
	if err := checkNew([]string{"mycheck"}, false); err == nil {
		t.Fatal("second new with same id must refuse overwrite")
	}
}

func TestCheckTestReadsFlowJSONFlagForms(t *testing.T) {
	// Given
	dir := t.TempDir()
	check := filepath.Join(dir, "passive.star")
	flow := filepath.Join(dir, "flow.json")
	if err := os.WriteFile(check, []byte("def check(flow):\n    return []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flow, []byte(`{"method":"GET","host":"example.test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "separate flag value", args: []string{check, "--flow-json", flow}},
		{name: "equals flag value", args: []string{check, "--flow-json=" + flow}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			err := checkTest(tt.args, false)

			// Then
			if err != nil {
				t.Fatalf("check test rejected --flow-json form: %v", err)
			}
		})
	}
}

func TestCheckAndRulesHelpReturnSuccess(t *testing.T) {
	// Given
	help := []string{"--help"}

	// When
	checkErr := runCheck(help)
	rulesErr := runRules(help)

	// Then
	if checkErr != nil || rulesErr != nil {
		t.Fatalf("help errors: check=%v rules=%v", checkErr, rulesErr)
	}
}

func TestUnknownCommandExitsWithoutStartingProxy(t *testing.T) {
	if os.Getenv("INTERSEPTOR_UNKNOWN_COMMAND_HELPER") == "1" {
		os.Args = []string{os.Args[0], "typo"}
		main()
		return
	}

	// Given
	command := exec.Command(os.Args[0], "-test.run=TestUnknownCommandExitsWithoutStartingProxy", "--", "typo")
	command.Env = append(os.Environ(), "INTERSEPTOR_UNKNOWN_COMMAND_HELPER=1")

	// When
	output, err := command.CombinedOutput()

	// Then
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("unknown command exit = %v, want exit 1; output=%s", err, output)
	}
	if got := string(output); !strings.Contains(got, `unknown command "typo"`) || strings.Contains(got, "control UI") {
		t.Fatalf("unknown command output = %q, want concise error without proxy startup", got)
	}
}

func TestCheckNewRejectsUnsafeID(t *testing.T) {
	// Given
	t.Setenv("INTERSEPTOR_DATA_DIR", t.TempDir())

	// When
	err := checkNew([]string{"../escape"}, false)

	// Then
	if err == nil {
		t.Fatal("unsafe check id created a file")
	}
}
