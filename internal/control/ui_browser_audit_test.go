package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestUIBrowserAuditEvidenceMatchesCurrentRuntime(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	reportBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs/ui-audit/browser-audit.json"))
	if err != nil {
		t.Fatalf("read retained UI browser audit: %v", err)
	}
	var report struct {
		ApplicationSource struct {
			RuntimeSHA256 string `json:"runtime_sha256"`
			RuntimeFiles  int    `json:"runtime_files"`
		} `json:"application_source"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode retained UI browser audit: %v", err)
	}

	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "go.mod", "go.sum", "cmd", "internal")
	cmd.Dir = repoRoot
	listed, err := cmd.Output()
	if err != nil {
		t.Fatalf("enumerate runtime source: %v", err)
	}
	unique := make(map[string]struct{})
	for _, raw := range bytes.Split(listed, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		relative := string(raw)
		if strings.HasSuffix(relative, "_test.go") {
			continue
		}
		info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("stat runtime source %s: %v", relative, err)
		}
		if info.Mode().IsRegular() {
			unique[relative] = struct{}{}
		}
	}
	paths := make([]string, 0, len(unique))
	for relative := range unique {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read runtime source %s: %v", relative, err)
		}
		_, _ = digest.Write([]byte(relative))
		_, _ = digest.Write([]byte{0})
		fileDigest := sha256.Sum256(content)
		_, _ = digest.Write(fileDigest[:])
	}

	gotDigest := hex.EncodeToString(digest.Sum(nil))
	if gotDigest != report.ApplicationSource.RuntimeSHA256 || len(paths) != report.ApplicationSource.RuntimeFiles {
		t.Fatalf("retained UI audit source identity is stale: got %s across %d files, evidence records %s across %d files", gotDigest, len(paths), report.ApplicationSource.RuntimeSHA256, report.ApplicationSource.RuntimeFiles)
	}
}

func TestUIBrowserAuditDigestExcludesIgnoredWorkspaceState(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`"git",`,
		`"ls-files",`,
		`"--cached"`,
		`"--others"`,
		`"--exclude-standard"`,
		`"-z"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("runtime identity must enumerate tracked or nonignored source files: missing %s", want)
		}
	}

	identityStart := strings.Index(text, "def runtime_source_identity()")
	if identityStart < 0 {
		t.Fatal("runtime_source_identity function not found")
	}
	identityEnd := strings.Index(text[identityStart:], "\ndef percentile(")
	if identityEnd < 0 {
		t.Fatal("runtime_source_identity function boundary not found")
	}
	identity := text[identityStart : identityStart+identityEnd]
	if strings.Contains(identity, ".rglob(") {
		t.Error("runtime identity must not hash ignored workspace files from a recursive filesystem walk")
	}
}

func TestUIBrowserAuditExpectedConsoleErrorsAreOneShot(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	if !strings.Contains(string(source), "result.expected_console_request_urls.remove(request_url)") {
		t.Error("an expected injected console error must not mask later failures at the same URL")
	}
}
