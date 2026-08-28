package version

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseWorkflowPinsOneGoReleaserVersion(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	text := string(workflow)

	const pin = "GORELEASER_VERSION: v2.18.0"
	if !strings.Contains(text, pin) {
		t.Fatalf("release workflow must declare the reviewed GoReleaser pin %q", pin)
	}
	const reference = "version: ${{ env.GORELEASER_VERSION }}"
	if got := strings.Count(text, reference); got != 2 {
		t.Fatalf("release workflow has %d shared GoReleaser version references, want 2", got)
	}
	if strings.Contains(text, "version: latest") {
		t.Fatal("release workflow must not resolve a moving GoReleaser version independently in candidate and publish jobs")
	}
}
