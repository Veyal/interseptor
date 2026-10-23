// Package chromemgr downloads, installs, and locates standalone Google Chrome
// for Testing binaries so Interseptor can open desktop app windows, print PDF
// reports, and launch test browsers without requiring manual system setup.
package chromemgr

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultMetadataURL is Google Chrome Labs' authoritative endpoint for last-known-good CfT releases.
const DefaultMetadataURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// ErrNotInstalled reports that managed Chrome is not installed in the target directory.
var ErrNotInstalled = errors.New("managed Chrome is not installed")

// ErrUnsupportedPlatform reports that the current OS/architecture is not supported by CfT.
var ErrUnsupportedPlatform = errors.New("unsupported platform for automated Chrome download")

// PlatformKey maps runtime.GOOS and runtime.GOARCH to Chrome for Testing platform names.
func PlatformKey(goos, goarch string) (string, error) {
	switch goos {
	case "darwin":
		switch goarch {
		case "arm64":
			return "mac-arm64", nil
		case "amd64":
			return "mac-x64", nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "linux64", nil
		case "arm64":
			return "linux-arm64", nil
		}
	case "windows":
		switch goarch {
		case "amd64", "arm64": // win64 runs on arm64 via emulation
			return "win64", nil
		case "386":
			return "win32", nil
		}
	}
	return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, goos, goarch)
}

// RelativeBinaryPath returns the expected executable location inside the
// extracted archive. Chrome for Testing archives unpack into a
// "chrome-<platform>" directory (chrome-mac-arm64/, chrome-linux64/, ...).
func RelativeBinaryPath(platform string) string {
	root := "chrome-" + platform
	switch {
	case strings.HasPrefix(platform, "mac-"):
		return filepath.Join(root, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	case strings.HasPrefix(platform, "win"):
		return filepath.Join(root, "chrome.exe")
	default:
		return filepath.Join(root, "chrome")
	}
}

// CandidatePaths returns all potential binary locations within chromeDir for a given platform.
func CandidatePaths(chromeDir, goos, goarch string) []string {
	if chromeDir == "" {
		return nil
	}
	platform, err := PlatformKey(goos, goarch)
	if err != nil {
		return nil
	}

	rel := RelativeBinaryPath(platform)
	candidates := []string{
		filepath.Join(chromeDir, rel),
	}

	// Also check direct unwrapped variations
	switch goos {
	case "darwin":
		candidates = append(candidates,
			filepath.Join(chromeDir, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
		)
	case "windows":
		candidates = append(candidates,
			filepath.Join(chromeDir, "chrome.exe"),
		)
	default:
		candidates = append(candidates,
			filepath.Join(chromeDir, "chrome"),
		)
	}
	return candidates
}

// BinaryPath checks if a managed Chrome binary is installed and executable in chromeDir.
func BinaryPath(chromeDir, goos, goarch string) (string, error) {
	candidates := CandidatePaths(chromeDir, goos, goarch)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			if goos != "windows" && fi.Mode().Perm()&0o111 == 0 {
				// Try to make it executable
				_ = os.Chmod(c, 0o755)
			}
			return c, nil
		}
	}
	return "", ErrNotInstalled
}

// Dir resolves the directory where managed Chrome is installed: globalDir,
// else INTERSEPTOR_DATA_DIR, else ~/.interseptor (or the legacy ~/.interceptor).
func Dir(globalDir string) string {
	if strings.TrimSpace(globalDir) == "" {
		globalDir = os.Getenv("INTERSEPTOR_DATA_DIR")
	}
	if strings.TrimSpace(globalDir) != "" {
		return filepath.Join(strings.TrimSpace(globalDir), "chrome")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "interseptor-chrome")
	}
	target := filepath.Join(home, ".interseptor", "chrome")
	if _, err := os.Stat(target); err == nil {
		return target
	}
	legacy := filepath.Join(home, ".interceptor", "chrome")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return target
}

// Installer downloads and unpacks Chrome for Testing binaries.
type Installer struct {
	MetadataURL string
	Client      *http.Client
	GOOS        string
	GOARCH      string
}

// NewInstaller returns an Installer configured for the current system. The
// client bounds connection setup only: the archive is ~150 MB, so a whole-request
// timeout would make slow links fail; the caller's context cancels the download.
func NewInstaller() *Installer {
	return &Installer{
		MetadataURL: DefaultMetadataURL,
		Client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		}},
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
	}
}

type cftMetadata struct {
	Channels struct {
		Stable struct {
			Version   string `json:"version"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
			} `json:"downloads"`
		} `json:"Stable"`
	} `json:"channels"`
}

// ResolveDownloadURL queries CfT metadata to find the stable download URL for the target platform.
func (i *Installer) ResolveDownloadURL(ctx context.Context, platform string) (version string, downloadURL string, err error) {
	url := i.MetadataURL
	if url == "" {
		url = DefaultMetadataURL
	}
	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("create metadata request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch CfT metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetch CfT metadata: HTTP %d", resp.StatusCode)
	}

	var meta cftMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", "", fmt.Errorf("decode CfT metadata: %w", err)
	}

	v := meta.Channels.Stable.Version
	for _, d := range meta.Channels.Stable.Downloads.Chrome {
		if d.Platform == platform && d.URL != "" {
			return v, d.URL, nil
		}
	}
	return "", "", fmt.Errorf("platform %q not found in Chrome for Testing stable channel", platform)
}

// Install downloads and extracts Chrome for Testing into targetDir.
// progress callback receives (bytesDone, totalBytes). logStep receives milestone messages.
func (i *Installer) Install(ctx context.Context, targetDir string, progress func(done, total int64), logStep func(format string, args ...any)) (string, error) {
	goos := i.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := i.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	platform, err := PlatformKey(goos, goarch)
	if err != nil {
		return "", err
	}

	if logStep != nil {
		logStep("Querying Chrome for Testing metadata for %s…", platform)
	}
	ver, dlURL, err := i.ResolveDownloadURL(ctx, platform)
	if err != nil {
		return "", fmt.Errorf("resolve Chrome download: %w", err)
	}

	if logStep != nil {
		logStep("Downloading Chrome for Testing v%s…", ver)
	}

	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download Chrome archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Chrome archive: HTTP %d", resp.StatusCode)
	}

	// Create temp file for the zip archive
	tmpZip, err := os.CreateTemp("", "cft-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp download file: %w", err)
	}
	defer os.Remove(tmpZip.Name())
	defer tmpZip.Close()

	total := resp.ContentLength
	var done int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmpZip.Write(buf[:n]); werr != nil {
				return "", fmt.Errorf("write temp archive: %w", werr)
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fmt.Errorf("read download stream: %w", rerr)
		}
	}

	if logStep != nil {
		logStep("Extracting to %s…", targetDir)
	}

	binPath, err := installArchive(ctx, tmpZip.Name(), targetDir, goos, goarch)
	if err != nil {
		return "", err
	}

	if logStep != nil {
		logStep("Installed Chrome for Testing v%s: %s", ver, binPath)
	}
	return binPath, nil
}

// installArchive extracts the archive into a staging directory beside
// targetDir and swaps it in only once the binary is present, so a failed or
// cancelled install never leaves a half-extracted Chrome that looks installed.
func installArchive(ctx context.Context, zipPath, targetDir, goos, goarch string) (string, error) {
	parent := filepath.Dir(filepath.Clean(targetDir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".chrome-stage-*")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o755); err != nil {
		return "", err
	}

	if err := extractZip(ctx, zipPath, stage); err != nil {
		return "", fmt.Errorf("extract Chrome archive: %w", err)
	}
	// On Darwin, strip quarantine attribute if present
	if goos == "darwin" {
		_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", stage).Run()
	}
	if _, err := BinaryPath(stage, goos, goarch); err != nil {
		return "", fmt.Errorf("locate installed binary: %w", err)
	}

	old := targetDir + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(targetDir, old); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("move previous install aside: %w", err)
	}
	if err := os.Rename(stage, targetDir); err != nil {
		_ = os.Rename(old, targetDir)
		return "", fmt.Errorf("activate install: %w", err)
	}
	_ = os.RemoveAll(old)

	binPath, err := BinaryPath(targetDir, goos, goarch)
	if err != nil {
		return "", fmt.Errorf("locate installed binary: %w", err)
	}
	if goos != "windows" {
		_ = os.Chmod(binPath, 0o755)
	}
	return binPath, nil
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func extractZip(ctx context.Context, zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	destDir = filepath.Clean(destDir)
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Prevent ZipSlip
		targetPath := filepath.Join(destDir, f.Name)
		if !within(destDir, targetPath) {
			return fmt.Errorf("illegal zip entry path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			if err := extractSymlink(f, destDir, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := extractFile(f, targetPath); err != nil {
			return err
		}
	}
	return nil
}

// extractSymlink recreates a symlink entry (the macOS app bundle relies on
// framework links such as Versions/Current), refusing targets outside destDir.
func extractSymlink(f *zip.File, destDir, targetPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(rc, 4096))
	rc.Close()
	if err != nil {
		return err
	}
	link := string(b)
	if link == "" || filepath.IsAbs(link) || !within(destDir, filepath.Join(filepath.Dir(targetPath), link)) {
		return fmt.Errorf("illegal zip symlink: %s -> %s", f.Name, link)
	}
	return os.Symlink(link, targetPath)
}

func extractFile(f *zip.File, targetPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	// Ensure binaries in MacOS or root are executable
	if strings.Contains(f.Name, "Contents/MacOS/") || strings.HasSuffix(f.Name, "/chrome") || f.Name == "chrome" {
		mode |= 0o755
	}
	out, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}
