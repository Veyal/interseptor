package version

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickAsset(t *testing.T) {
	rel := &releaseInfo{
		Tag: "v1.2.3",
		Assets: []releaseAsset{
			{Name: "interseptor_1.2.3_linux_amd64.tar.gz", URL: "https://ex/linux"},
			{Name: "interseptor_1.2.3_darwin_arm64.tar.gz", URL: "https://ex/darwin"},
			{Name: "checksums.txt", URL: "https://ex/sums"},
		},
	}
	name, url := pickAssetFor(rel, "1.2.3", "linux", "amd64")
	if name != "interseptor_1.2.3_linux_amd64.tar.gz" || url != "https://ex/linux" {
		t.Fatalf("linux: %q %q", name, url)
	}
	name, url = pickAssetFor(rel, "1.2.3", "darwin", "arm64")
	if url != "https://ex/darwin" {
		t.Fatalf("darwin: %q %q", name, url)
	}
}

func pickAssetFor(rel *releaseInfo, version, goos, goarch string) (string, string) {
	candidates := assetCandidates(version, goos, goarch)
	byName := map[string]string{}
	for _, a := range rel.Assets {
		byName[strings.ToLower(a.Name)] = a.URL
	}
	for _, c := range candidates {
		if u, ok := byName[strings.ToLower(c)]; ok {
			return c, u
		}
	}
	osToken, archToken := platformTokens(goos, goarch)
	for _, a := range rel.Assets {
		low := strings.ToLower(a.Name)
		if !strings.HasSuffix(low, ".tar.gz") && !strings.HasSuffix(low, ".zip") {
			continue
		}
		if strings.Contains(low, osToken) && strings.Contains(low, archToken) {
			return a.Name, a.URL
		}
	}
	return "", ""
}

func TestUntarGz(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "interseptor", Mode: 0o755, Size: 3})
	_, _ = tw.Write([]byte("bin"))
	_ = tw.Close()
	_ = gw.Close()

	got, err := untarGz(buf.Bytes())
	if err != nil || string(got) != "bin" {
		t.Fatalf("untarGz: %q err=%v", got, err)
	}
}

func TestUntarGzRejectsBinaryOverExpandedLimit(t *testing.T) {
	// Given
	archive := tarGzBinary(t, 1025)

	// When
	_, err := untarGzWithLimits(archive, archiveReadLimits{entries: 8, fileBytes: 1024, totalBytes: 4096})

	// Then
	if err == nil || !strings.Contains(err.Error(), "expanded size limit") {
		t.Fatalf("untarGz error=%v, want expanded size limit", err)
	}
}

func TestUntarGzAcceptsBinaryAtExpandedLimit(t *testing.T) {
	// Given
	archive := tarGzBinary(t, 1024)

	// When
	binary, err := untarGzWithLimits(archive, archiveReadLimits{entries: 8, fileBytes: 1024, totalBytes: 4096})

	// Then
	if err != nil || len(binary) != 1024 {
		t.Fatalf("untarGz bytes=%d err=%v", len(binary), err)
	}
}

func TestUnzipBinRejectsTooManyEntries(t *testing.T) {
	// Given
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 5; i++ {
		w, err := zw.Create(fmt.Sprintf("extra-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// When
	_, err := unzipBinWithLimits(buf.Bytes(), archiveReadLimits{entries: 4, fileBytes: 1024, totalBytes: 4096})

	// Then
	if err == nil || !strings.Contains(err.Error(), "entry count limit") {
		t.Fatalf("unzipBin error=%v, want entry count limit", err)
	}
}

func tarGzBinary(t *testing.T, size int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "interseptor", Mode: 0o755, Size: size}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(tw, zeroReader{}, size); err != nil {
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

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	if err := verifySHA256(data, hexSum); err != nil {
		t.Fatal(err)
	}
	if verifySHA256(data, "deadbeef") == nil {
		t.Fatal("expected mismatch")
	}
}

func TestFormatBytes(t *testing.T) {
	if formatBytes(512) != "512 B" {
		t.Fatalf("512 B")
	}
	if formatBytes(2<<20) != "2.0 MB" {
		t.Fatalf("2 MiB")
	}
}

func TestProgressReaderReports(t *testing.T) {
	var buf bytes.Buffer
	prog := &updateProgress{out: &buf, term: false}
	data := bytes.Repeat([]byte("x"), 3<<20)
	_, err := io.ReadAll(&progressReader{r: bytes.NewReader(data), prog: prog, total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "downloading:") {
		t.Fatalf("expected progress output, got %q", buf.String())
	}
}

func TestPostInstallNotesAreSilentForNormalUpdate(t *testing.T) {
	var buf bytes.Buffer

	printPostInstallNotes(&buf, "", "", false)

	if got := buf.String(); got != "" {
		t.Fatalf("normal update printed post-install notes: %q", got)
	}
}

func TestAssetCandidates(t *testing.T) {
	c := assetCandidates("0.7.0", "linux", "amd64")
	if len(c) < 2 || c[0] != "interseptor_0.7.0_linux_amd64.tar.gz" {
		t.Fatalf("unexpected candidates: %v", c)
	}
}

func TestRebrandInstallPath(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "interceptor")
	got, ok := rebrandInstallPath(in)
	want := filepath.Join(dir, "interseptor")
	if !ok || got != want {
		t.Fatalf("got (%q,%v), want (%q,true)", got, ok, want)
	}
	exeIn := filepath.Join(dir, "interceptor.exe")
	got, ok = rebrandInstallPath(exeIn)
	want = filepath.Join(dir, "interseptor.exe")
	if !ok || got != want {
		t.Fatalf("exe: got (%q,%v), want (%q,true)", got, ok, want)
	}
	same := filepath.Join(dir, "interseptor")
	got, ok = rebrandInstallPath(same)
	if ok || got != same {
		t.Fatalf("already rebranded: got (%q,%v)", got, ok)
	}
}

func TestIsLegacyBinaryName(t *testing.T) {
	if !isLegacyBinaryName("interceptor") || !isLegacyBinaryName("Interceptor.exe") {
		t.Fatal("expected legacy names")
	}
	if isLegacyBinaryName("interseptor") || isLegacyBinaryName("interseptor.exe") {
		t.Fatal("interseptor is not legacy")
	}
}

func TestInstallLegacyShimSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "interseptor")
	legacy := filepath.Join(dir, "interceptor")
	if err := os.WriteFile(target, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installLegacyShim(legacy, target); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		got, err := os.Readlink(legacy)
		if err != nil || got != target {
			t.Fatalf("symlink -> %q err=%v, want %q", got, err, target)
		}
		return
	}
	// Fallback wrapper script.
	b, _ := os.ReadFile(legacy)
	if !strings.Contains(string(b), target) {
		t.Fatalf("wrapper missing target: %s", b)
	}
}

func TestWindowsUpdateScript(t *testing.T) {
	script := windowsUpdateScript(
		`C:\tools\interseptor.exe.new`,
		`C:\tools\interseptor.exe`,
		`C:\tools\interseptor-update.bat`,
		`C:\tools\interseptor-update.log`,
		"",
	)
	for _, want := range []string{
		`set "NEW=C:\tools\interseptor.exe.new"`,
		`taskkill /PID %%p /T /F`,
		`:retry`,
		`if !TRY! GEQ 90 goto fail`,
		`start "" "%DEST%"`,
		`interseptor-update.log`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if !strings.Contains(script, "timeout /t 3 /nobreak") {
		t.Fatal("expected 3s initial wait for update CLI to exit")
	}
}

func TestWindowsUpdateScriptRebrand(t *testing.T) {
	script := windowsUpdateScript(
		`C:\tools\interseptor.exe.new`,
		`C:\tools\interseptor.exe`,
		`C:\tools\interseptor-update.bat`,
		`C:\tools\interseptor-update.log`,
		`C:\tools\interceptor.exe`,
	)
	for _, want := range []string{
		`set "LEGACY=C:\tools\interceptor.exe"`,
		`interceptor.bat`,
		`interseptor.exe`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("rebrand script missing %q:\n%s", want, script)
		}
	}
}
