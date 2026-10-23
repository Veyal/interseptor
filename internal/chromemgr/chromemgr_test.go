package chromemgr

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlatformKey(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{"darwin", "arm64", "mac-arm64", false},
		{"darwin", "amd64", "mac-x64", false},
		{"linux", "amd64", "linux64", false},
		{"linux", "arm64", "linux-arm64", false},
		{"windows", "amd64", "win64", false},
		{"windows", "386", "win32", false},
		{"freebsd", "amd64", "", true},
		{"linux", "mips", "", true},
	}
	for _, tc := range tests {
		got, err := PlatformKey(tc.goos, tc.goarch)
		if tc.wantErr && err == nil {
			t.Errorf("PlatformKey(%q, %q) expected error, got nil", tc.goos, tc.goarch)
		}
		if !tc.wantErr && (err != nil || got != tc.want) {
			t.Errorf("PlatformKey(%q, %q) = (%q, %v), want (%q, nil)", tc.goos, tc.goarch, got, err, tc.want)
		}
	}
}

func TestResolveDownloadURLFromMetadata(t *testing.T) {
	mockJSON := `{
		"timestamp": "2026-10-21T00:00:00.000Z",
		"channels": {
			"Stable": {
				"channel": "Stable",
				"version": "156.0.8078.47",
				"downloads": {
					"chrome": [
						{"platform": "linux64", "url": "https://example.com/chrome-linux64.zip"},
						{"platform": "mac-arm64", "url": "https://example.com/chrome-mac-arm64.zip"},
						{"platform": "win64", "url": "https://example.com/chrome-win64.zip"}
					]
				}
			}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, mockJSON)
	}))
	defer srv.Close()

	inst := &Installer{
		MetadataURL: srv.URL,
		Client:      srv.Client(),
	}

	ver, url, err := inst.ResolveDownloadURL(context.Background(), "mac-arm64")
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	if ver != "156.0.8078.47" {
		t.Errorf("version = %q, want 156.0.8078.47", ver)
	}
	if url != "https://example.com/chrome-mac-arm64.zip" {
		t.Errorf("url = %q, want https://example.com/chrome-mac-arm64.zip", url)
	}

	// Missing platform
	_, _, err = inst.ResolveDownloadURL(context.Background(), "mac-x64")
	if err == nil {
		t.Error("expected error for missing platform, got nil")
	}
}

func TestInstallExtractsAndSetsExecutablePermissions(t *testing.T) {
	// Build a mock zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	relPath := RelativeBinaryPath("mac-arm64")
	f, err := zw.Create(relPath)
	if err != nil {
		t.Fatalf("create zip member: %v", err)
	}
	fmt.Fprint(f, "#!/bin/sh\necho 'Google Chrome for Testing 156.0.8078.47'\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	zipBytes := buf.Bytes()

	// Server that returns metadata and the zip file
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"channels": map[string]any{
					"Stable": map[string]any{
						"version": "156.0.8078.47",
						"downloads": map[string]any{
							"chrome": []map[string]string{
								{"platform": "mac-arm64", "url": "http://" + r.Host + "/download.zip"},
							},
						},
					},
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer srv.Close()

	destDir := t.TempDir()
	inst := &Installer{
		MetadataURL: srv.URL + "/metadata.json",
		Client:      srv.Client(),
		GOOS:        "darwin",
		GOARCH:      "arm64",
	}

	var logs []string
	logStep := func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	bin, err := inst.Install(context.Background(), destDir, nil, logStep)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary mode %o is not executable", fi.Mode().Perm())
	}
	if len(logs) == 0 {
		t.Error("expected log steps, got none")
	}

	// Verify BinaryPath finds it
	found, err := BinaryPath(destDir, "darwin", "arm64")
	if err != nil {
		t.Fatalf("BinaryPath failed: %v", err)
	}
	if found != bin {
		t.Errorf("BinaryPath = %q, want %q", found, bin)
	}
}

func TestCandidatePathsAbsolute(t *testing.T) {
	dir := "/tmp/interseptor-test/chrome"
	candidates := CandidatePaths(dir, "darwin", "arm64")
	if len(candidates) == 0 {
		t.Fatal("expected candidate paths for darwin/arm64")
	}
	for _, c := range candidates {
		if !filepath.IsAbs(c) {
			t.Errorf("candidate path is not absolute: %q", c)
		}
	}
}

// serveCfT serves CfT metadata pointing at /download.zip and the given archive.
func serveCfT(t *testing.T, platform string, zipBytes []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"channels": map[string]any{"Stable": map[string]any{
					"version": "156.0.8078.47",
					"downloads": map[string]any{"chrome": []map[string]string{
						{"platform": platform, "url": "http://" + r.Host + "/download.zip"},
					}},
				}},
			})
			return
		}
		w.Write(zipBytes)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type zipEntry struct {
	name, body string
	mode       os.FileMode
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, e.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func linuxInstaller(srv *httptest.Server) *Installer {
	return &Installer{MetadataURL: srv.URL + "/m.json", Client: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
}

func TestInstallFailureLeavesNoUsableBinary(t *testing.T) {
	good := buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}, {name: "chrome-linux64/extra.pak", body: strings.Repeat("x", 4096)}})
	// Corrupt the second member's compressed data so extraction fails midway.
	bad := append([]byte(nil), good...)
	idx := bytes.Index(bad, []byte("chrome-linux64/extra.pak")) // local header: data follows the name
	for i := idx + len("chrome-linux64/extra.pak"); i < idx+len("chrome-linux64/extra.pak")+16; i++ {
		bad[i] ^= 0xff
	}
	srv := serveCfT(t, "linux64", bad)
	dest := filepath.Join(t.TempDir(), "chrome")
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err == nil {
		t.Fatal("Install of a corrupt archive succeeded")
	}
	if p, err := BinaryPath(dest, "linux", "amd64"); err == nil {
		t.Fatalf("BinaryPath = %q after a failed install, want ErrNotInstalled", p)
	}
}

func TestInstallReplacesPreviousInstall(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "chrome")
	stale := filepath.Join(dest, "chrome-linux64", "stale.pak")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}}))
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file from the previous install survived: %v", err)
	}
}

func TestInstallKeepsSymlinksAndRejectsEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{
		{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755},
		{name: "chrome-linux64/lib/real.so", body: "lib"},
		{name: "chrome-linux64/lib/current.so", body: "real.so", mode: os.ModeSymlink | 0o777},
	}))
	dest := filepath.Join(t.TempDir(), "chrome")
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	link := filepath.Join(dest, "chrome-linux64", "lib", "current.so")
	if target, err := os.Readlink(link); err != nil || target != "real.so" {
		t.Fatalf("Readlink(%s) = %q, %v; want real.so", link, target, err)
	}

	for _, target := range []string{"../../../../etc/passwd", "/etc/passwd"} {
		srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{
			{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755},
			{name: "chrome-linux64/evil", body: target, mode: os.ModeSymlink | 0o777},
		}))
		if _, err := linuxInstaller(srv).Install(context.Background(), filepath.Join(t.TempDir(), "chrome"), nil, nil); err == nil {
			t.Errorf("Install accepted a symlink to %q", target)
		}
	}
}

func TestDirHonoursDataDirEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("INTERSEPTOR_DATA_DIR", root)
	if got, want := Dir(""), filepath.Join(root, "chrome"); got != want {
		t.Fatalf("Dir(\"\") = %q, want %q", got, want)
	}
}

func TestNewInstallerHasNoWholeDownloadTimeout(t *testing.T) {
	// Client.Timeout covers reading the ~150 MB body, so slow links could never finish.
	if c := NewInstaller().Client; c != nil && c.Timeout != 0 {
		t.Fatalf("Client.Timeout = %v, want 0 (use transport/connect timeouts instead)", c.Timeout)
	}
}

// The layout of the real Chrome for Testing archives (checked against the
// published 156 zips): every platform unpacks into chrome-<platform>/.
func TestRelativeBinaryPathMatchesRealArchiveLayout(t *testing.T) {
	for platform, want := range map[string]string{
		"mac-arm64": "chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
		"mac-x64":   "chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
		"linux64":   "chrome-linux64/chrome",
		"win64":     "chrome-win64/chrome.exe",
		"win32":     "chrome-win32/chrome.exe",
	} {
		if got := filepath.ToSlash(RelativeBinaryPath(platform)); got != want {
			t.Errorf("RelativeBinaryPath(%q) = %q, want %q", platform, got, want)
		}
	}
}
