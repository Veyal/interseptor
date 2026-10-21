package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/chromemgr"
)

func TestRunInstallChrome(t *testing.T) {
	tempDataDir := t.TempDir()

	// Build a mock zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	platform, err := chromemgr.PlatformKey(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip("unsupported test platform")
	}
	relPath := chromemgr.RelativeBinaryPath(platform)
	f, err := zw.Create(relPath)
	if err != nil {
		t.Fatalf("create zip member: %v", err)
	}
	fmt.Fprint(f, "#!/bin/sh\necho test\n")
	_ = zw.Close()
	zipBytes := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"channels": map[string]any{
					"Stable": map[string]any{
						"version": "156.0.8078.47",
						"downloads": map[string]any{
							"chrome": []map[string]string{
								{"platform": platform, "url": "http://" + r.Host + "/download.zip"},
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

	oldInstaller := newCLIChromeInstaller
	defer func() { newCLIChromeInstaller = oldInstaller }()
	newCLIChromeInstaller = func() *chromemgr.Installer {
		return &chromemgr.Installer{
			MetadataURL: srv.URL + "/metadata.json",
			Client:      srv.Client(),
			GOOS:        runtime.GOOS,
			GOARCH:      runtime.GOARCH,
		}
	}

	// 1. Initial install
	if err := runInstallChrome([]string{"--data-dir", tempDataDir}); err != nil {
		t.Fatalf("initial runInstallChrome: %v", err)
	}

	bin, err := chromemgr.BinaryPath(filepath.Join(tempDataDir, "chrome"), runtime.GOOS, runtime.GOARCH)
	if err != nil || bin == "" {
		t.Fatalf("expected binary installed, got bin=%q err=%v", bin, err)
	}

	// 2. Already installed, skips download
	if err := runInstallChrome([]string{"--data-dir", tempDataDir}); err != nil {
		t.Fatalf("subsequent runInstallChrome: %v", err)
	}

	// 3. Force install
	if err := runInstallChrome([]string{"--data-dir", tempDataDir, "--force"}); err != nil {
		t.Fatalf("forced runInstallChrome: %v", err)
	}
}

func TestNormalizeInstallChromeFlag(t *testing.T) {
	in := []string{"--install_chrome"}
	got := normalizeCLIArgs(in)
	if len(got) != 1 || got[0] != "--install-chrome" {
		t.Errorf("normalizeCLIArgs(%v) = %v, want [--install-chrome]", in, got)
	}
}

func TestReexecArgsOmitsInstallChrome(t *testing.T) {
	in := []string{"--install-chrome", "--project", "test"}
	got := reexecArgs(in, "newproj")
	for _, a := range got {
		if strings.Contains(a, "install-chrome") {
			t.Errorf("reexecArgs included --install-chrome: %v", got)
		}
	}
}
