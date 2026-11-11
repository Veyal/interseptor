//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/chromemgr"
)

// Ctrl-C during `interseptor install-chrome` cancels the download and removes
// what it had staged, instead of killing the process mid-write.
func TestInstallChromeStopsOnInterrupt(t *testing.T) {
	platform, err := chromemgr.PlatformKey(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip("unsupported test platform")
	}
	release := make(chan struct{})
	var once sync.Once
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_ = json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{
				"version":   "1.2.3.4",
				"downloads": map[string]any{"chrome": []map[string]string{{"platform": platform, "url": srv.URL + "/download.zip"}}},
			}}})
			return
		}
		// The download has started, so the command's signal handling is armed.
		w.Header().Set("Content-Length", "1048576")
		w.Write(make([]byte, 4096))
		w.(http.Flusher).Flush()
		once.Do(func() { _ = syscall.Kill(os.Getpid(), syscall.SIGINT) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	old := newCLIChromeInstaller
	defer func() { newCLIChromeInstaller = old }()
	newCLIChromeInstaller = func() *chromemgr.Installer {
		return &chromemgr.Installer{MetadataURL: srv.URL + "/m.json", Client: srv.Client()}
	}

	dataDir := t.TempDir()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	done := make(chan error, 1)
	go func() { done <- runInstallChrome([]string{"--data-dir", dataDir}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an interrupted install reported success")
		}
		if !isInterrupted(err) {
			t.Errorf("error does not report the interruption: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("install-chrome ignored SIGINT")
	}
	for _, pattern := range []string{filepath.Join(dataDir, ".chrome-stage-*"), filepath.Join(tmp, "cft-*.zip")} {
		if left, _ := filepath.Glob(pattern); len(left) != 0 {
			t.Errorf("left behind after the interrupt: %v", left)
		}
	}
}
