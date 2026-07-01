package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestPickAsset(t *testing.T) {
	rel := &releaseInfo{
		Tag: "v1.2.3",
		Assets: []releaseAsset{
			{Name: "interceptor_1.2.3_linux_amd64.tar.gz", URL: "https://ex/linux"},
			{Name: "interceptor_1.2.3_darwin_arm64.tar.gz", URL: "https://ex/darwin"},
			{Name: "checksums.txt", URL: "https://ex/sums"},
		},
	}
	name, url := pickAssetFor(rel, "1.2.3", "linux", "amd64")
	if name != "interceptor_1.2.3_linux_amd64.tar.gz" || url != "https://ex/linux" {
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
	_ = tw.WriteHeader(&tar.Header{Name: "interceptor", Mode: 0o755, Size: 3})
	_, _ = tw.Write([]byte("bin"))
	_ = tw.Close()
	_ = gw.Close()

	got, err := untarGz(buf.Bytes())
	if err != nil || string(got) != "bin" {
		t.Fatalf("untarGz: %q err=%v", got, err)
	}
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

func TestAssetCandidates(t *testing.T) {
	c := assetCandidates("0.7.0", "linux", "amd64")
	if len(c) < 2 || c[0] != "interceptor_0.7.0_linux_amd64.tar.gz" {
		t.Fatalf("unexpected candidates: %v", c)
	}
}

func TestWindowsUpdateScript(t *testing.T) {
	script := windowsUpdateScript(
		`C:\tools\interceptor.exe.new`,
		`C:\tools\interceptor.exe`,
		`C:\tools\interceptor-update.bat`,
		`C:\tools\interceptor-update.log`,
	)
	for _, want := range []string{
		`set "NEW=C:\tools\interceptor.exe.new"`,
		`taskkill /PID %%p /T /F`,
		`:retry`,
		`if !TRY! GEQ 90 goto fail`,
		`start "" "%DEST%"`,
		`interceptor-update.log`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if !strings.Contains(script, "timeout /t 3 /nobreak") {
		t.Fatal("expected 3s initial wait for update CLI to exit")
	}
}
