package version

import (
	"os"
	"regexp"
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

func TestMacOSBuildStampsBundleVersionIntoBinaries(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile("../../packaging/macos/build-app.sh")
	if err != nil {
		t.Fatalf("read macOS build script: %v", err)
	}

	const stamp = "-X github.com/Veyal/interseptor/internal/version.Version=$SHORT_VERSION"
	if !strings.Contains(string(script), stamp) {
		t.Fatalf("macOS build script must stamp the bundle version with %q", stamp)
	}
}

func TestMacOSBuildFallbackAcceptsDeclaredVersion(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile("../../packaging/macos/build-app.sh")
	if err != nil {
		t.Fatalf("read macOS build script: %v", err)
	}
	versionSource, err := os.ReadFile("version.go")
	if err != nil {
		t.Fatalf("read version source: %v", err)
	}

	declaration := regexp.MustCompile(`(?m)^[\t ]*(?:const|var)[\t ]+Version[\t ]*=[\t ]*"([^"]+)"`)
	match := declaration.FindSubmatch(versionSource)
	if len(match) != 2 {
		t.Fatal("version source must contain a supported Version declaration")
	}

	const portableFallback = `(const|var)[[:space:]]+Version`
	if !strings.Contains(string(script), portableFallback) {
		t.Fatalf("macOS shallow-checkout fallback must accept the real %q declaration using %q", match[0], portableFallback)
	}
	if strings.Contains(string(script), `s/^const Version`) {
		t.Fatal("macOS shallow-checkout fallback must not assume Version is declared as a const")
	}
}

func TestReleaseWorkflowVerifiesExactMacOSBundleVersion(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	text := string(workflow)

	for _, contract := range []string{
		`expected_version="interseptor v${GITHUB_REF_NAME#v}"`,
		`actual_version="$("$APP/Contents/MacOS/interseptor" version)"`,
		`[[ "$actual_version" == "$expected_version" ]]`,
	} {
		if !strings.Contains(text, contract) {
			t.Errorf("release workflow must contain exact macOS version check %q", contract)
		}
	}
}

func TestReleaseWorkflowPublishesMacOSChecksums(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	text := string(workflow)

	for _, contract := range []string{
		"sha256sum",
		"sha256sum -c macos-checksums.txt",
		"validated-macos/macos-checksums.txt",
	} {
		if !strings.Contains(text, contract) {
			t.Errorf("release workflow must generate, verify, and upload macOS checksums; missing %q", contract)
		}
	}
}
