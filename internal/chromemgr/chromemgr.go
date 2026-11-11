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
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/proc"
)

// DefaultMetadataURL is Google Chrome Labs' authoritative endpoint for last-known-good CfT releases.
const DefaultMetadataURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// The archive is fetched from the Chrome for Testing bucket. Google publishes no
// checksum or signature for it (the metadata lists a platform and a URL only),
// so what can be enforced is where it comes from and how large it is. (A
// damaged transfer is still caught: every zip member carries a CRC-32 that the
// extractor checks.)
const (
	officialDownloadHost   = "storage.googleapis.com"
	officialDownloadPrefix = "/chrome-for-testing-public/"
	// maxMetadataBytes bounds the metadata document (about 10 KB today).
	maxMetadataBytes = 4 << 20
)

// Size bounds: the archive is about 200 MB and unpacks to under 1 GB today.
// Variables so tests can lower them.
var (
	maxArchiveBytes  int64 = 1 << 30
	maxUnpackedBytes int64 = 4 << 30
)

// installLockPoll is how long one attempt to take the install lock waits before
// the caller's context is checked again.
const installLockPoll = 200 * time.Millisecond

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read CfT metadata: %w", err)
	}
	if len(body) > maxMetadataBytes {
		return "", "", fmt.Errorf("CfT metadata is larger than %d bytes", maxMetadataBytes)
	}
	var meta cftMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
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

// validateDownloadURL decides whether the archive may be fetched from raw. With
// the official metadata endpoint only the Chrome for Testing bucket over HTTPS
// qualifies: the metadata is plain JSON, so a tampered or mistaken entry must
// not be able to point the download (an executable that is then run) at another
// host or at another tenant's bucket on the same host. A configured mirror may
// serve the archive only from its own origin.
func (i *Installer) validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("download URL %q: %w", raw, err)
	}
	if u.User != nil || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("download URL %q is not a plain http(s) URL", raw)
	}
	metaURL := i.MetadataURL
	if metaURL == "" {
		metaURL = DefaultMetadataURL
	}
	if metaURL == DefaultMetadataURL {
		if u.Scheme != "https" || u.Host != officialDownloadHost {
			return fmt.Errorf("download URL %q is not on https://%s", raw, officialDownloadHost)
		}
		if !strings.HasPrefix(u.Path, officialDownloadPrefix) || path.Clean(u.Path) != u.Path || u.RawPath != "" {
			return fmt.Errorf("download URL %q is outside the Chrome for Testing bucket", raw)
		}
		return nil
	}
	m, err := url.Parse(metaURL)
	if err != nil {
		return fmt.Errorf("metadata URL %q: %w", metaURL, err)
	}
	if u.Scheme != m.Scheme || u.Host != m.Host {
		return fmt.Errorf("download URL %q is not on the metadata origin %s://%s", raw, m.Scheme, m.Host)
	}
	return nil
}

// downloadClient is i.Client with every redirect hop held to the same rule as
// the download URL itself, so a redirect cannot carry the fetch elsewhere.
func (i *Installer) downloadClient() *http.Client {
	base := i.Client
	if base == nil {
		base = http.DefaultClient
	}
	c := *base
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return i.validateDownloadURL(req.URL.String())
	}
	return &c
}

// lockInstall takes the cross-process install lock for targetDir, waiting for
// another install (a second `interseptor install-chrome`, or the UI's button in
// a running server) to finish, or for ctx to end. Without it two installs
// shared `<target>.old` and each could delete or roll back the other's swap.
func lockInstall(ctx context.Context, targetDir string) (*proc.FileLock, error) {
	lockPath := filepath.Clean(targetDir) + ".lock"
	for {
		lock, err := proc.LockFile(lockPath, installLockPoll)
		if err == nil {
			return lock, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("waiting for another Chrome install to finish: %w", ctx.Err())
		}
		if !errors.Is(err, proc.ErrLockHeld) {
			return nil, err
		}
	}
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
	// One install at a time per directory, across processes.
	lock, err := lockInstall(ctx, targetDir)
	if err != nil {
		return "", err
	}
	defer lock.Unlock()

	ver, dlURL, err := i.ResolveDownloadURL(ctx, platform)
	if err != nil {
		return "", fmt.Errorf("resolve Chrome download: %w", err)
	}
	if err := i.validateDownloadURL(dlURL); err != nil {
		return "", fmt.Errorf("resolve Chrome download: %w", err)
	}

	if logStep != nil {
		logStep("Downloading Chrome for Testing v%s…", ver)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := i.downloadClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("download Chrome archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download Chrome archive: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArchiveBytes {
		return "", fmt.Errorf("Chrome archive is too large: %d bytes (limit %d)", resp.ContentLength, maxArchiveBytes)
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
			if done+int64(n) > maxArchiveBytes {
				return "", fmt.Errorf("Chrome archive is too large: more than %d bytes", maxArchiveBytes)
			}
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
	// The compressed size is bounded by the download; this bounds what it may
	// expand to, so a small archive cannot fill the disk.
	remaining := maxUnpackedBytes
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Prevent ZipSlip
		targetPath := filepath.Join(destDir, f.Name)
		if !within(destDir, targetPath) {
			return fmt.Errorf("illegal zip entry path: %s", f.Name)
		}

		// The lexical check above cannot see symlinks earlier members created:
		// "a/up" -> ".." and "a/out" -> "up/.." are each inside destDir on
		// paper, yet "a/out/x" lands outside it. No member may be created
		// through, or on top of, a symlink.
		if err := refuseSymlinkPath(destDir, targetPath); err != nil {
			return fmt.Errorf("illegal zip entry path: %s: %w", f.Name, err)
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
		if err := extractFile(f, targetPath, &remaining); err != nil {
			return err
		}
	}
	return nil
}

// refuseSymlinkPath reports an error when any existing path component between
// destDir (exclusive) and targetPath (inclusive) is a symlink. destDir itself
// may legitimately sit below a symlink (/tmp on macOS) and is not inspected.
func refuseSymlinkPath(destDir, targetPath string) error {
	rel, err := filepath.Rel(destDir, targetPath)
	if err != nil {
		return err
	}
	cur := destDir
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil // nothing below a missing component exists yet
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", cur)
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

// extractFile writes one member, drawing its size from *remaining (the bytes
// the whole archive may still unpack to).
func extractFile(f *zip.File, targetPath string, remaining *int64) error {
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
	n, err := io.Copy(out, io.LimitReader(rc, *remaining+1))
	if err == nil && n > *remaining {
		err = fmt.Errorf("archive unpacks to more than %d bytes", maxUnpackedBytes)
	}
	*remaining -= n
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}
