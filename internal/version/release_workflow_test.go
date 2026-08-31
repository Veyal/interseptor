package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("BSD/POSIX sed extractor is not available on Windows")
	}
	t.Parallel()

	script, err := os.ReadFile("../../packaging/macos/build-app.sh")
	if err != nil {
		t.Fatalf("read macOS build script: %v", err)
	}

	extractorPattern := regexp.MustCompile(`(?ms)^[\t ]*SHORT_VERSION="\$\(sed -nE[[:space:]]+\\[[:space:]]*'([^']+)'[[:space:]]+\\[[:space:]]*"\$REPO_ROOT/internal/version/version\.go"[[:space:]]*\|[[:space:]]*head -1\)"`)
	extractors := extractorPattern.FindAllSubmatch(script, -1)
	if len(extractors) != 1 {
		t.Fatalf("macOS build script has %d executable version extractors, want 1", len(extractors))
	}

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "const", source: "package version\n\nconst Version = \"1.2.3\"\n", want: "1.2.3"},
		{name: "var", source: "package version\n\n\tvar\tVersion = \"4.5.6\"\n", want: "4.5.6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := filepath.Join(t.TempDir(), "version.go")
			if err := os.WriteFile(fixture, []byte(tc.source), 0o600); err != nil {
				t.Fatalf("write version fixture: %v", err)
			}

			output, err := exec.Command("sed", "-nE", string(extractors[0][1]), fixture).CombinedOutput()
			if err != nil {
				t.Fatalf("run version extractor: %v: %s", err, output)
			}
			if got, want := string(output), tc.want+"\n"; got != want {
				t.Fatalf("version extractor output = %q, want %q", got, want)
			}
		})
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
