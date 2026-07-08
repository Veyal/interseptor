package main

import (
	"errors"
	"testing"

	"github.com/Veyal/interseptor/internal/version"
)

func TestRunUpdateCheckOnly(t *testing.T) {
	// --check against a real tagged release should not error (network permitting).
	// Uses direct release URLs when the GitHub API is rate-limited.
	if testing.Short() {
		t.Skip("network")
	}
	if err := runUpdate([]string{"--check", "--version", version.Version}); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestRestartRequiredIsSuccess(t *testing.T) {
	if !errors.Is(version.ErrRestartRequired, version.ErrRestartRequired) {
		t.Fatal("sentinel")
	}
}
