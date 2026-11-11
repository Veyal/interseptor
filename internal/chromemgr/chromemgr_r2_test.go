package chromemgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// With the official metadata endpoint, the archive may only come from the
// Chrome for Testing bucket over HTTPS. The metadata is unsigned JSON and no
// digest is published, so the URL it names must not be followed anywhere.
func TestValidateDownloadURLPinsTheOfficialBucket(t *testing.T) {
	official := &Installer{MetadataURL: DefaultMetadataURL}
	for _, ok := range []string{
		"https://storage.googleapis.com/chrome-for-testing-public/157.0.8103.50/linux64/chrome-linux64.zip",
	} {
		if err := official.validateDownloadURL(ok); err != nil {
			t.Errorf("validateDownloadURL(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{
		"http://storage.googleapis.com/chrome-for-testing-public/x/linux64/chrome-linux64.zip", // not TLS
		"https://storage.googleapis.com/some-other-bucket/chrome-linux64.zip",                  // another tenant's bucket
		"https://storage.googleapis.com/chrome-for-testing-public/../other/chrome.zip",
		"https://storage.googleapis.com.example.com/chrome-for-testing-public/chrome.zip",
		"https://user@storage.googleapis.com/chrome-for-testing-public/chrome.zip",
		"https://storage.googleapis.com:8443/chrome-for-testing-public/chrome.zip",
		"https://example.com/chrome-linux64.zip",
		"file:///etc/passwd",
		"https://127.0.0.1/chrome.zip",
		"",
	} {
		if err := official.validateDownloadURL(bad); err == nil {
			t.Errorf("validateDownloadURL(%q) accepted", bad)
		}
	}
	// An empty MetadataURL means the default endpoint and gets the same pin.
	if err := (&Installer{}).validateDownloadURL("https://example.com/chrome.zip"); err == nil {
		t.Error("the zero Installer accepted an arbitrary host")
	}
	// A configured mirror may serve the archive itself, but nothing else may.
	mirror := &Installer{MetadataURL: "http://127.0.0.1:9/m.json"}
	if err := mirror.validateDownloadURL("http://127.0.0.1:9/download.zip"); err != nil {
		t.Errorf("mirror same-origin download refused: %v", err)
	}
	if err := mirror.validateDownloadURL("http://127.0.0.1:10/download.zip"); err == nil {
		t.Error("mirror metadata redirected the download to another origin")
	}
}

// Install refuses a download URL outside the allowed origin before fetching it.
func TestInstallRefusesForeignDownloadHost(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits.Add(1)
		w.Write(buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}}))
	}))
	defer foreign.Close()
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{
			"version":   "1.2.3.4",
			"downloads": map[string]any{"chrome": []map[string]string{{"platform": "linux64", "url": foreign.URL + "/chrome.zip"}}},
		}}})
	}))
	defer meta.Close()

	dest := filepath.Join(t.TempDir(), "chrome")
	_, err := linuxInstaller(meta).Install(context.Background(), dest, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "download URL") {
		t.Fatalf("Install = %v, want a refused download URL", err)
	}
	if n := foreignHits.Load(); n != 0 {
		t.Errorf("the foreign host was contacted %d time(s)", n)
	}
}

// A redirect must not carry the download to another origin either.
func TestInstallRefusesRedirectToForeignHost(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}}))
	}))
	defer foreign.Close()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_ = json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{
				"version":   "1.2.3.4",
				"downloads": map[string]any{"chrome": []map[string]string{{"platform": "linux64", "url": srv.URL + "/download.zip"}}},
			}}})
			return
		}
		http.Redirect(w, r, foreign.URL+"/chrome.zip", http.StatusFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "chrome")
	if _, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil); err == nil {
		t.Fatal("Install followed a redirect to another origin")
	}
	if _, err := BinaryPath(dest, "linux", "amd64"); err == nil {
		t.Fatal("an archive from the redirect target was installed")
	}
}

// The archive is bounded: by its declared length, and by what actually arrives
// when the server declares none (chunked).
func TestInstallBoundsTheDownload(t *testing.T) {
	orig := maxArchiveBytes
	maxArchiveBytes = 64 << 10
	t.Cleanup(func() { maxArchiveBytes = orig })

	for _, chunked := range []bool{false, true} {
		var sent atomic.Int64
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".json") {
				_ = json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{
					"version":   "1.2.3.4",
					"downloads": map[string]any{"chrome": []map[string]string{{"platform": "linux64", "url": srv.URL + "/download.zip"}}},
				}}})
				return
			}
			const total = 8 << 20
			if !chunked {
				w.Header().Set("Content-Length", "8388608")
			}
			block := make([]byte, 32<<10)
			for n := 0; n < total; n += len(block) {
				if _, err := w.Write(block); err != nil {
					return
				}
				sent.Add(int64(len(block)))
				if chunked {
					w.(http.Flusher).Flush()
				}
			}
		}))
		dest := filepath.Join(t.TempDir(), "chrome")
		_, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("chunked=%v: Install = %v, want a size refusal", chunked, err)
		}
		if got := sent.Load(); got > 4<<20 {
			t.Errorf("chunked=%v: %d bytes were pulled before giving up (limit %d)", chunked, got, maxArchiveBytes)
		}
	}
}

// A zip bomb is stopped by the unpacked-size bound, and nothing is installed.
func TestInstallBoundsTheUnpackedSize(t *testing.T) {
	orig := maxUnpackedBytes
	maxUnpackedBytes = 1 << 20
	t.Cleanup(func() { maxUnpackedBytes = orig })

	bomb := buildZip(t, []zipEntry{
		{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755},
		{name: "chrome-linux64/a.pak", body: strings.Repeat("\x00", 900<<10)},
		{name: "chrome-linux64/b.pak", body: strings.Repeat("\x00", 900<<10)},
	})
	if len(bomb) > 64<<10 {
		t.Fatalf("fixture is not a bomb: %d bytes", len(bomb))
	}
	srv := serveCfT(t, "linux64", bomb)
	dest := filepath.Join(t.TempDir(), "chrome")
	_, err := linuxInstaller(srv).Install(context.Background(), dest, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Fatalf("Install = %v, want an unpacked-size refusal", err)
	}
	if _, err := BinaryPath(dest, "linux", "amd64"); err == nil {
		t.Fatal("a partial install was activated")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".chrome-stage-*"))
	if len(left) != 0 {
		t.Errorf("staging directories left behind: %v", left)
	}
}

// Two installs into the same directory (two CLI runs, or the CLI and the UI
// button) take turns: each one's download, extraction and swap is not
// interleaved with the other's, and both end with a working install.
func TestConcurrentInstallsAreSerialised(t *testing.T) {
	archive := buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}})
	var inFlight, peak atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_ = json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{
				"version":   "1.2.3.4",
				"downloads": map[string]any{"chrome": []map[string]string{{"platform": "linux64", "url": srv.URL + "/download.zip"}}},
			}}})
			return
		}
		n := inFlight.Add(1)
		for {
			if p := peak.Load(); n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		w.Write(archive)
		inFlight.Add(-1)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "chrome")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = linuxInstaller(srv).Install(context.Background(), dest, nil, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("install %d: %v", i, err)
		}
	}
	if p := peak.Load(); p != 1 {
		t.Errorf("%d installs downloaded at the same time, want them serialised", p)
	}
	if _, err := BinaryPath(dest, "linux", "amd64"); err != nil {
		t.Errorf("no usable install afterwards: %v", err)
	}
	for _, pattern := range []string{".chrome-stage-*", "chrome.old"} {
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), pattern)); len(left) != 0 {
			t.Errorf("left behind: %v", left)
		}
	}
}

// A waiting install gives up when its context is cancelled instead of queueing forever.
func TestInstallWaitingForTheLockHonoursCancel(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "chrome")
	held, err := lockInstall(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	srv := serveCfT(t, "linux64", buildZip(t, []zipEntry{{name: "chrome-linux64/chrome", body: "#!/bin/sh\n", mode: 0o755}}))
	start := time.Now()
	if _, err := linuxInstaller(srv).Install(ctx, dest, nil, nil); err == nil {
		t.Fatal("Install proceeded while another install held the lock")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Install waited %s after its context was cancelled", d)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the target directory was touched without the lock")
	}
}

// The metadata document is small; an endless one must not be read into memory.
func TestResolveDownloadURLBoundsTheMetadata(t *testing.T) {
	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"channels":{"Stable":{"version":"`))
		block := []byte(strings.Repeat("9", 64<<10))
		for i := 0; i < 2048; i++ { // 128 MiB if nobody stops it
			if _, err := w.Write(block); err != nil {
				return
			}
			sent.Add(int64(len(block)))
		}
	}))
	defer srv.Close()
	i := &Installer{MetadataURL: srv.URL + "/m.json", Client: srv.Client()}
	if _, _, err := i.ResolveDownloadURL(context.Background(), "linux64"); err == nil {
		t.Fatal("an oversized metadata document was accepted")
	}
	if got := sent.Load(); got > 32<<20 {
		t.Errorf("%d bytes of metadata were read", got)
	}
}
