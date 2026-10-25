// Package desktopinstall downloads the native macOS app (Interseptor.app) from
// a GitHub release, verifies it the way the app's own updater does, and
// installs it into an Applications folder with an atomic swap.
//
// The zip is fetched with a Go HTTP client, so it carries no quarantine
// attribute and the ad-hoc signed, un-notarized app opens without the
// Gatekeeper "Open Anyway" step.
package desktopinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/version"
)

const (
	// AppName is the bundle directory inside the release zip and the target folder.
	AppName = "Interseptor.app"
	// BundleID is the CFBundleIdentifier set by packaging/macos/build-app.sh.
	BundleID = "com.veyal.interseptor"
	// ChecksumsName is the sha256sum file the release workflow attaches for macOS assets.
	ChecksumsName = "macos-checksums.txt"

	maxChecksumsBytes = 1 << 20
	maxZipBytes       = 1 << 30
)

var (
	// ErrUnsupportedOS is returned on any OS other than macOS.
	ErrUnsupportedOS = errors.New("the Interseptor desktop app is macOS-only; on this OS run `interseptor`, which opens the UI in its own window")
	// ErrAppRunning is returned while the app at the target path is running.
	ErrAppRunning = errors.New("Interseptor.app is running")
	// ErrDowngrade is returned when a newer app is installed and Force is off.
	ErrDowngrade = errors.New("a newer Interseptor.app is already installed")
)

// ZipName is the release asset name of the app zip for tag (e.g. "v2.19.0").
func ZipName(tag string) string { return "Interseptor-" + tag + "-unsigned.zip" }

// downloadHosts mirror UpdateHosts.downloadHosts in packaging/macos/shell/Versioning.swift.
var downloadHosts = map[string]bool{
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// TrustedDownloadHost reports whether release bytes may come from host.
func TrustedDownloadHost(host string) bool { return downloadHosts[strings.ToLower(host)] }

// Runner runs an external tool and returns its combined output and exit code.
// err is non-nil only when the tool could not be started.
type Runner func(ctx context.Context, name string, args ...string) (out []byte, code int, err error)

// Options selects what to install and where.
type Options struct {
	Dir     string // Applications folder; empty = /Applications if writable, else ~/Applications
	Version string // release version; empty = latest stable release
	Force   bool   // reinstall the same version or downgrade
}

// Result describes the outcome. Installed is false when the requested version
// was already present and nothing changed.
type Result struct {
	Path      string
	Version   string
	Installed bool
}

// Installer holds every external dependency so tests never touch the network
// or a real Applications folder.
type Installer struct {
	GOOS        string
	Client      *http.Client
	Latest      func(ctx context.Context) (string, error)
	AssetURL    func(tag, name string) string
	TrustedHost func(host string) bool
	Run         Runner
	SystemDir   string
	HomeDir     func() (string, error)
	Writable    func(dir string) bool
	Rename      func(oldPath, newPath string) error
	Out         io.Writer
}

// New returns an Installer wired to GitHub releases and the real system.
func New(out io.Writer) *Installer {
	return &Installer{
		GOOS:   runtime.GOOS,
		Client: newHTTPClient(),
		Latest: func(ctx context.Context) (string, error) {
			latest, _, err := version.CheckLatestFresh(ctx)
			return latest, err
		},
		AssetURL:    version.ReleaseAssetURL,
		TrustedHost: TrustedDownloadHost,
		Run:         execRunner,
		SystemDir:   "/Applications",
		HomeDir:     os.UserHomeDir,
		Writable:    version.DirWritable,
		Rename:      os.Rename,
		Out:         out,
	}
}

// newHTTPClient has connect/TLS/header timeouts but no whole-request timeout:
// the app zip is large and a slow link must not be cut off mid-download.
func newHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}}
}

func execRunner(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// Install resolves, downloads, verifies and installs the app.
func (i *Installer) Install(ctx context.Context, opts Options) (Result, error) {
	if i.GOOS != "darwin" {
		return Result{}, ErrUnsupportedOS
	}
	dir, err := i.targetDir(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	ver, err := i.resolveVersion(ctx, opts.Version)
	if err != nil {
		return Result{}, err
	}
	target := filepath.Join(dir, AppName)
	res := Result{Path: target, Version: ver}
	if done, err := i.checkExisting(ctx, target, ver, opts.Force); done || err != nil {
		return res, err
	}
	i.logf("Installing Interseptor v%s into %s…", ver, dir)
	if err := i.install(ctx, dir, target, ver); err != nil {
		return res, err
	}
	res.Installed = true
	i.logf("Installed Interseptor v%s at %s", ver, target)
	return res, nil
}

func (i *Installer) logf(format string, args ...any) {
	if i.Out != nil {
		fmt.Fprintf(i.Out, format+"\n", args...)
	}
}

// targetDir picks the Applications folder: an explicit absolute dir, else
// the system folder when writable, else ~/Applications (created if missing).
func (i *Installer) targetDir(explicit string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fmt.Errorf("--dir must be an absolute path, got %q", explicit)
		}
		dir := filepath.Clean(explicit)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		return dir, nil
	}
	if i.Writable(i.SystemDir) {
		return i.SystemDir, nil
	}
	home, err := i.HomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("%s is not writable and the home folder is unknown; pass --dir", i.SystemDir)
	}
	dir := filepath.Join(home, "Applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

func (i *Installer) resolveVersion(ctx context.Context, requested string) (string, error) {
	ver := strings.TrimPrefix(strings.TrimSpace(requested), "v")
	if ver == "" {
		i.logf("Resolving the latest release…")
		latest, err := i.Latest(ctx)
		if err != nil {
			return "", fmt.Errorf("find the latest release: %w", err)
		}
		ver = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	}
	// The version becomes part of the release URL and the asset file name, so
	// only a plain release version is accepted.
	if !releaseVersion.MatchString(ver) {
		return "", fmt.Errorf("invalid release version %q", ver)
	}
	return ver, nil
}

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

// checkExisting decides what to do about an app already at target. done is
// true when the requested version is already installed.
func (i *Installer) checkExisting(ctx context.Context, target, ver string, force bool) (bool, error) {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return false, nil
	}
	info, infoErr := readInfo(ctx, i.Run, target)
	if infoErr != nil && !force {
		return false, fmt.Errorf("%s exists but is not a readable app bundle; pass --force to replace it", target)
	}
	if !force {
		if info.ID != BundleID {
			return false, fmt.Errorf("%s is a different app (%s); pass --force to replace it", target, info.ID)
		}
		if cmp, ok := version.CompareVersions(info.Version, ver); ok && cmp == 0 {
			i.logf("Interseptor v%s is already installed at %s (pass --force to reinstall)", info.Version, target)
			return true, nil
		} else if ok && cmp > 0 {
			return false, fmt.Errorf("%w (v%s at %s); pass --force to install v%s anyway", ErrDowngrade, info.Version, target, ver)
		}
	}
	return false, i.ensureNotRunning(ctx, target)
}

// ensureNotRunning refuses while any process runs an executable inside the bundle.
func (i *Installer) ensureNotRunning(ctx context.Context, app string) error {
	// Match both the path as given and its symlink-resolved form (/var vs
	// /private/var), case-insensitively as APFS is (/applications ==
	// /Applications).
	paths := []string{regexp.QuoteMeta(filepath.Join(app, "Contents", "MacOS") + "/")}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(app)); err == nil {
		if r := filepath.Join(resolved, filepath.Base(app)); r != app {
			paths = append(paths, regexp.QuoteMeta(filepath.Join(r, "Contents", "MacOS")+"/"))
		}
	}
	pattern := "^(" + strings.Join(paths, "|") + ")"
	out, code, err := i.Run(ctx, "/usr/bin/pgrep", "-i", "-f", pattern)
	switch {
	case err != nil:
		return fmt.Errorf("check whether %s is running: %w", app, err)
	case code == 1:
		return nil
	case code == 0:
		return fmt.Errorf("%w (pid %s). Quit Interseptor first, then run this again", ErrAppRunning, strings.Join(strings.Fields(string(out)), ", "))
	default:
		return fmt.Errorf("check whether %s is running: pgrep exit %d: %s", app, code, strings.TrimSpace(string(out)))
	}
}

// install downloads and verifies into temp/staging dirs and swaps the app in.
// Every exit path removes the temp and staging directories.
func (i *Installer) install(ctx context.Context, dir, target, ver string) error {
	tag := "v" + ver
	zipName := ZipName(tag)
	sum, err := i.fetchChecksum(ctx, tag, zipName)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "interseptor-desktop-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	zipPath := filepath.Join(tmp, zipName)
	if err := i.downloadVerified(ctx, tag, zipName, zipPath, sum); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".interseptor-install-*")
	if err != nil {
		return fmt.Errorf("create staging folder in %s: %w", dir, err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	app, err := i.extractAndVerify(ctx, zipPath, stage, ver)
	if err != nil {
		return err
	}
	if err := i.ensureNotRunning(ctx, target); err != nil {
		return err
	}
	keep, err = i.swap(app, target, stage)
	return err
}

// fetchChecksum returns the SHA-256 listed for exactly zipName. It fails
// closed: no checksum file or no exact line means nothing is downloaded.
func (i *Installer) fetchChecksum(ctx context.Context, tag, zipName string) (string, error) {
	i.logf("Fetching %s…", ChecksumsName)
	var buf strings.Builder
	if err := i.get(ctx, i.AssetURL(tag, ChecksumsName), &buf, maxChecksumsBytes, nil); err != nil {
		return "", fmt.Errorf("release %s has no macOS app checksums (%w)", tag, err)
	}
	sum, ok := version.ParseChecksums(buf.String(), zipName)
	if !ok {
		return "", fmt.Errorf("refusing to install: %s has no entry for %s", ChecksumsName, zipName)
	}
	return sum, nil
}

func (i *Installer) downloadVerified(ctx context.Context, tag, zipName, dest, want string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if err := i.get(ctx, i.AssetURL(tag, zipName), io.MultiWriter(f, h), maxZipBytes, i.progress(zipName)); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	i.logf("Verifying SHA-256…")
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, release lists %s", zipName, got, want)
	}
	return nil
}

// progress prints a line every 10%, like install-chrome.
func (i *Installer) progress(name string) func(done, total int64) {
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct/10 != last/10 || (pct == 100 && last != 100) {
			last = pct
			i.logf("Downloading %s: %d%% (%d / %d MB)", name, pct, done>>20, total>>20)
		}
	}
}

// get downloads rawURL after checking it, and every redirect, against the host policy.
func (i *Installer) get(ctx context.Context, rawURL string, w io.Writer, limit int64, report func(done, total int64)) error {
	if err := i.checkURL(rawURL); err != nil {
		return err
	}
	client := *i.Client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return i.checkURL(req.URL.String())
	}
	_, _, err := version.DownloadTo(ctx, &client, rawURL, w, limit, report)
	return err
}

func (i *Installer) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	trusted := i.TrustedHost
	if trusted == nil {
		trusted = TrustedDownloadHost
	}
	if u.Scheme != "https" || u.User != nil || !trusted(u.Host) {
		return fmt.Errorf("refusing untrusted download URL %s (only https GitHub release hosts)", u.Redacted())
	}
	return nil
}
