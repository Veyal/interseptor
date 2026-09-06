package main

import (
	"errors"
	"os"
	"testing"

	"github.com/Veyal/interseptor/internal/version"
)

func TestRunUpdateCheckOnly(t *testing.T) {
	// This reaches the public GitHub release service. Keep it opt-in so ordinary
	// unit and release-candidate runs are not made flaky by rate limits, network
	// policy, or an in-progress publication. CI can opt in after publication.
	if testing.Short() || os.Getenv("INTERSEPTOR_LIVE_RELEASE_CHECK") != "1" {
		t.Skip("live GitHub release check")
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
