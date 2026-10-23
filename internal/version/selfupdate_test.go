package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestDetectInstall(t *testing.T) {
	writable := func(string) bool { return true }
	readonly := func(string) bool { return false }
	cases := []struct {
		name, exe, resolved, ver string
		writable                 func(string) bool
		want                     InstallMethod
		command                  string
	}{
		{"plain binary", "/home/u/bin/interseptor", "/home/u/bin/interseptor", "2.0.0", writable, InstallSelf, ""},
		{"brew arm symlink", "/opt/homebrew/bin/interseptor", "/opt/homebrew/Cellar/interseptor/2.0.0/bin/interseptor", "2.0.0", writable, InstallBrew, "brew upgrade interseptor"},
		{"brew intel cellar", "/usr/local/bin/interseptor", "/usr/local/Cellar/interseptor/2.0.0/bin/interseptor", "2.0.0", writable, InstallBrew, "brew upgrade interseptor"},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/interseptor", "/home/linuxbrew/.linuxbrew/Cellar/interseptor/2.0.0/bin/interseptor", "2.0.0", writable, InstallBrew, "brew upgrade interseptor"},
		{"scoop", `C:\Users\u\scoop\shims\interseptor.exe`, `C:\Users\u\scoop\apps\interseptor\current\interseptor.exe`, "2.0.0", writable, InstallScoop, "scoop update interseptor"},
		{"desktop bundle", "/Applications/Interseptor.app/Contents/MacOS/interseptor", "/Applications/Interseptor.app/Contents/MacOS/interseptor", "2.0.0", writable, InstallDesktop, ""},
		{"desktop via symlink", "/usr/local/bin/interseptor", "/Applications/Interseptor.app/Contents/MacOS/interseptor", "2.0.0", writable, InstallDesktop, ""},
		{"desktop wins over dev", "/Applications/Interseptor.app/Contents/MacOS/interseptor", "", "2.0.0-local", writable, InstallDesktop, ""},
		{"local build", "/tmp/interseptor", "/tmp/interseptor", "2.18.1-local", writable, InstallDev, ""},
		{"dev suffix", "/tmp/interseptor", "/tmp/interseptor", "2.18.1-dev", writable, InstallDev, ""},
		{"bare dev", "/tmp/interseptor", "/tmp/interseptor", "dev", writable, InstallDev, ""},
		{"readonly dir", "/usr/bin/interseptor", "/usr/bin/interseptor", "2.0.0", readonly, InstallReadonly, ""},
	}
	for _, c := range cases {
		got := DetectInstall(c.exe, c.resolved, c.ver, c.writable)
		if got.Method != c.want || got.Command != c.command {
			t.Errorf("%s: got %+v, want method %q command %q", c.name, got, c.want, c.command)
		}
		if got.Method != InstallSelf && got.Reason == "" {
			t.Errorf("%s: a refused method must explain itself", c.name)
		}
		if got.Method == InstallSelf && got.Reason != "" {
			t.Errorf("%s: self install should carry no refusal reason, got %q", c.name, got.Reason)
		}
	}
}

func TestDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !DirWritable(dir) {
		t.Fatal("temp dir should be writable")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("probe left files behind: %v", entries)
	}
	if DirWritable(filepath.Join(dir, "missing")) {
		t.Fatal("a missing directory is not writable")
	}
}

// fakeRelease serves a GoReleaser-shaped release (archive + checksums.txt) for
// the running platform and points the package's download root at it.
type fakeRelease struct {
	srv     *httptest.Server
	archive []byte
	sums    string
}

func newFakeRelease(t *testing.T, ver string, bin []byte, corruptSum bool) *fakeRelease {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INTERSEPTOR_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	name := assetCandidates(ver, runtime.GOOS, runtime.GOARCH)[0]
	f := &fakeRelease{archive: tarGzBytes(t, "interseptor", bin)}
	sum := sha256.Sum256(f.archive)
	hexSum := hex.EncodeToString(sum[:])
	if corruptSum {
		hexSum = strings.Repeat("0", 64)
	}
	f.sums = hexSum + "  " + name + "\n"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+name):
			w.Header().Set("Content-Length", strconv.Itoa(len(f.archive)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(f.archive)
			}
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(f.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	old := githubReleasesDownload
	githubReleasesDownload = f.srv.URL + "/download"
	t.Cleanup(func() { githubReleasesDownload = old })
	return f
}

func tarGzBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeExe(t *testing.T, content string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "interseptor")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

type progressLog struct {
	mu     sync.Mutex
	phases []string
	last   StageProgress
}

func (p *progressLog) add(sp StageProgress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.phases); n == 0 || p.phases[n-1] != sp.Phase {
		p.phases = append(p.phases, sp.Phase)
	}
	p.last = sp
}

func TestStageInstallsVerifiedBinaryOnUnix(t *testing.T) {
	newFakeRelease(t, "9.9.9", []byte("NEW-BINARY"), false)
	exe := writeExe(t, "OLD")
	var log progressLog
	staged, err := Stage(context.Background(), StageOptions{Version: "9.9.9", Exe: exe, GOOS: "linux", Progress: log.add})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "NEW-BINARY" {
		t.Fatalf("binary = %q, want the new release", got)
	}
	if staged.Dest != exe || staged.Staged != "" || staged.Version != "9.9.9" {
		t.Fatalf("staged = %+v", staged)
	}
	if want := []string{PhaseDownloading, PhaseVerifying, PhaseInstalling}; strings.Join(log.phases, ",") != strings.Join(want, ",") {
		t.Fatalf("phases = %v, want %v", log.phases, want)
	}
	if log.last.Total <= 0 || log.last.Done != log.last.Total {
		t.Fatalf("download progress not reported to completion: %+v", log.last)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("staging file left behind")
	}
}

func TestStageChecksumMismatchReplacesNothing(t *testing.T) {
	newFakeRelease(t, "9.9.9", []byte("EVIL"), true)
	exe := writeExe(t, "OLD")
	_, err := Stage(context.Background(), StageOptions{Version: "9.9.9", Exe: exe, GOOS: "linux"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "OLD" {
		t.Fatalf("binary replaced after a failed verification: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("unexpected files after failure: %v", entries)
	}
}

func TestStageHonorsCancellation(t *testing.T) {
	newFakeRelease(t, "9.9.9", []byte("NEW"), false)
	exe := writeExe(t, "OLD")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Stage(ctx, StageOptions{Version: "9.9.9", Exe: exe, GOOS: "linux"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "OLD" {
		t.Fatal("canceled update replaced the binary")
	}
}

func TestStageWindowsStagesThenSwapsOnFinish(t *testing.T) {
	newFakeRelease(t, "9.9.9", []byte("NEW-EXE"), false)
	exe := writeExe(t, "RUNNING")
	staged, err := Stage(context.Background(), StageOptions{Version: "9.9.9", Exe: exe, GOOS: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "RUNNING" {
		t.Fatal("windows staging must not touch the running executable")
	}
	if staged.Staged != exe+".new" {
		t.Fatalf("staged path = %q", staged.Staged)
	}
	if err := FinishStaged(staged); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "NEW-EXE" {
		t.Fatalf("after finish binary = %q", got)
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "RUNNING" {
		t.Fatalf("previous binary should be parked at .old, got %q", got)
	}
	CleanupReplacedBinary(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("cleanup left the parked binary")
	}
}

func TestFinishStagedIsNoopWhenInstalledInPlace(t *testing.T) {
	if err := FinishStaged(StagedUpdate{Dest: "/nonexistent/interseptor"}); err != nil {
		t.Fatal(err)
	}
}

func TestFetchReleaseNotes(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/x/releases/tags/v9.9.9" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","body":"## Fixed\n- a bug","html_url":"https://github.com/Veyal/interseptor/releases/tag/v9.9.9"}`))
	}))
	defer srv.Close()
	old := githubAPIRoot
	githubAPIRoot = srv.URL + "/repos/x"
	defer func() { githubAPIRoot = old }()

	n, err := FetchReleaseNotes(context.Background(), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if n.Body != "## Fixed\n- a bug" || !strings.HasSuffix(n.URL, "/v9.9.9") {
		t.Fatalf("notes = %+v", n)
	}
	// A missing release still yields a usable link to the release page.
	n, err = FetchReleaseNotes(context.Background(), "1.0.0")
	if err == nil || n.URL != ReleasePageURL("1.0.0") {
		t.Fatalf("missing release: %+v %v", n, err)
	}
}

func TestIsNewer(t *testing.T) {
	if !IsNewer("1.0.0", "1.1.0") || IsNewer("1.1.0", "1.1.0") || IsNewer("2.0.0", "1.9.9") {
		t.Fatal("IsNewer disagrees with semantic ordering")
	}
}

// Only GoReleaser's release builds self-update in place; a source build with
// the same fallback version is reported as a development build.
func TestSourceBuildGuard(t *testing.T) {
	self := InstallInfo{Method: InstallSelf}
	if got := sourceBuildGuard(self, "release"); got.Method != InstallSelf {
		t.Fatalf("release build = %s, want self", got.Method)
	}
	if got := sourceBuildGuard(self, ""); got.Method != InstallDev {
		t.Fatalf("source build = %s, want dev", got.Method)
	}
	if got := sourceBuildGuard(InstallInfo{Method: InstallReadonly}, ""); got.Method != InstallDev {
		t.Fatalf("readonly source build = %s, want dev", got.Method)
	}
	for _, m := range []InstallMethod{InstallBrew, InstallScoop, InstallDesktop} {
		if got := sourceBuildGuard(InstallInfo{Method: m}, ""); got.Method != m {
			t.Fatalf("%s must keep its method, got %s", m, got.Method)
		}
	}
}

func TestGoReleaserStampsReleaseChannel(t *testing.T) {
	b, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const want = "-X github.com/Veyal/interseptor/internal/version.Channel=release"
	if n := strings.Count(string(b), want); n < 2 {
		t.Fatalf("both GoReleaser builds must stamp %q (found %d)", want, n)
	}
}

// A symlink, directory or empty file named like the binary is refused instead
// of installing a broken executable.
func TestUpdateArchiveRequiresRegularNonEmptyBinary(t *testing.T) {
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "interseptor", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"})
	_ = tw.Close()
	_ = gz.Close()
	if _, err := untarGz(tb.Bytes()); err == nil {
		t.Fatal("a symlinked binary was accepted")
	}
	tb.Reset()
	gz = gzip.NewWriter(&tb)
	tw = tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "interseptor", Typeflag: tar.TypeReg, Mode: 0o755, Size: 0})
	_ = tw.Close()
	_ = gz.Close()
	if _, err := untarGz(tb.Bytes()); err == nil {
		t.Fatal("an empty binary was accepted")
	}
}
