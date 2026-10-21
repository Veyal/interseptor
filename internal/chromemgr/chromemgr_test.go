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
