package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/chromemgr"
)

func TestChromeStatusHandler(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/chrome")
	if err != nil {
		t.Fatalf("get /api/chrome: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		t.Fatalf("status = %d, want %d: %v", resp.StatusCode, http.StatusOK, errRes)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := res["available"]; !ok {
		t.Error("missing available field in response")
	}
	if _, ok := res["managedDir"]; !ok {
		t.Error("missing managedDir field in response")
	}
}

func TestChromeInstallHandler(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Build a mock zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	platform, _ := chromemgr.PlatformKey(runtime.GOOS, runtime.GOARCH)
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

	customDir := t.TempDir()
	t.Setenv("INTERSEPTOR_DATA_DIR", customDir)

	// Inject test installer
	oldInstaller := newChromeInstaller
	defer func() { newChromeInstaller = oldInstaller }()
	newChromeInstaller = func() *chromemgr.Installer {
		return &chromemgr.Installer{
			MetadataURL: srv.URL + "/metadata.json",
			Client:      srv.Client(),
			GOOS:        runtime.GOOS,
			GOARCH:      runtime.GOARCH,
		}
	}

	resp, err := http.Post(ts.URL+"/api/chrome/install", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post /api/chrome/install: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		t.Fatalf("status = %d, want %d: %v", resp.StatusCode, http.StatusOK, errRes)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("ok = %v, want true", res["ok"])
	}
	path, _ := res["path"].(string)
	if path == "" {
		t.Fatal("empty path returned")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("installed binary not found on disk: %v", err)
	}
}
