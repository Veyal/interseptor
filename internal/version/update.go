package version

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrRestartRequired means the new binary is staged and the process should exit
// so a Windows updater script can replace the running executable.
var ErrRestartRequired = errors.New("restart required to finish update")

// UpdateOptions configures a self-update run.
type UpdateOptions struct {
	Version string // empty = latest tag
	Check   bool   // only report availability
	Force   bool   // reinstall even when up to date
	Out     io.Writer
}

// releaseAsset is one file attached to a GitHub release.
type releaseAsset struct {
	Name string
	URL  string
}

// releaseInfo is the subset of a GitHub release we need for updating.
type releaseInfo struct {
	Tag    string
	Assets []releaseAsset
}

// Update checks GitHub for a newer (or requested) release and installs it.
// It prefers a prebuilt binary asset for the current OS/arch; if none is
// attached to the release it falls back to `go install` when the Go toolchain
// is available.
func Update(ctx context.Context, opts UpdateOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}
	prog := newUpdateProgress(out)
	target := strings.TrimSpace(opts.Version)
	if target != "" {
		target = strings.TrimPrefix(target, "v")
	} else {
		prog.step("Checking for latest release…")
		latest, newer, err := CheckLatest(ctx)
		if err != nil {
			return fmt.Errorf("check for updates: %w", err)
		}
		if latest == "" {
			return fmt.Errorf("no releases found on GitHub")
		}
		target = latest
		if !opts.Force && !newer && String() == latest {
			fmt.Fprintf(out, "interseptor v%s is already up to date\n", String())
			return nil
		}
		if opts.Check {
			cur := String()
			if cur == latest {
				fmt.Fprintf(out, "interseptor v%s is up to date\n", cur)
			} else {
				fmt.Fprintf(out, "update available: v%s (you have v%s)\n", latest, cur)
			}
			return nil
		}
	}

	if opts.Check {
		prog.step("Checking release v%s…", target)
		rel, err := fetchRelease(ctx, target)
		if err != nil {
			return err
		}
		cur := String()
		ver := strings.TrimPrefix(rel.Tag, "v")
		if cur == ver {
			fmt.Fprintf(out, "interseptor v%s is up to date\n", cur)
		} else {
			fmt.Fprintf(out, "update available: v%s (you have v%s)\n", ver, cur)
		}
		return nil
	}

	if strings.TrimSpace(opts.Version) != "" {
		prog.step("Fetching release v%s…", target)
	}
	rel, err := fetchRelease(ctx, target)
	if err != nil {
		return err
	}
	ver := strings.TrimPrefix(rel.Tag, "v")
	if !opts.Force && ver == String() {
		fmt.Fprintf(out, "interseptor v%s is already up to date\n", String())
		return nil
	}

	cur := String()
	if cur != ver {
		prog.step("Found v%s (you have v%s)", ver, cur)
	} else {
		prog.step("Reinstalling v%s", ver)
	}

	if name, url := pickAsset(rel, ver); url != "" {
		prog.step("Downloading %s…", name)
		data, err := download(ctx, url, prog)
		if err != nil {
			return err
		}
		prog.downloadDone()
		if sum, ok := checksumFor(rel, name); ok {
			prog.step("Verifying checksum…")
			if err := verifySHA256(data, sum); err != nil {
				return err
			}
		}
		prog.step("Extracting binary…")
		bin, err := extractBinary(data, name)
		if err != nil {
			return err
		}
		dest, err := os.Executable()
		if err != nil {
			return err
		}
		dest, err = filepath.EvalSymlinks(dest)
		if err != nil {
			return err
		}
		installDest, legacyPath, rebranded := resolveInstallDest(dest)
		if rebranded {
			prog.step("Rebranding interceptor → interseptor…")
		}
		prog.step("Installing to %s…", installDest)
		if err := installBinary(installDest, bin, legacyPath); err != nil {
			if errors.Is(err, ErrRestartRequired) {
				prog.done("Windows updater started — it will stop running Interseptor instances, replace the binary, and restart automatically")
				fmt.Fprintf(out, "\nIf the app does not restart within a minute, run `interseptor stop`, then move %s.new over %s manually.\n", filepath.Base(installDest), installDest)
				return err
			}
			return err
		}
		prog.done("Updated to interseptor v%s → %s", ver, installDest)
		if rebranded {
			printRebrandNotice(out, legacyPath, installDest)
		}
		printMCPUpdateNote(out)
		return nil
	}

	prog.step("No prebuilt binary for %s/%s — running go install…", runtime.GOOS, runtime.GOARCH)
	if err := goInstall(ctx, ver, out); err != nil {
		return fmt.Errorf("%w\n\ninstall manually: https://github.com/%s/releases/tag/v%s", err, Repo, ver)
	}
	gopath, _ := exec.LookPath("go")
	_ = gopath
	if bin, err := goInstallBin(); err == nil {
		prog.done("Installed interseptor v%s via go install → %s", ver, bin)
	} else {
		prog.done("Installed interseptor v%s via go install (ensure $(go env GOPATH)/bin is on your PATH)", ver)
	}
	printMCPUpdateNote(out)
	return nil
}

func printMCPUpdateNote(out io.Writer) {
	fmt.Fprintf(out, "\nMCP: if Cursor uses Streamable HTTP (http://127.0.0.1:9966/mcp), restart Interseptor to pick up this build — no MCP config change needed.\n")
	fmt.Fprintf(out, "     stdio clients: restart the MCP server or use scripts/interseptor-mcp to resolve the updated binary on PATH.\n")
}

// isLegacyBinaryName reports whether base is the pre-rename product binary name.
func isLegacyBinaryName(base string) bool {
	switch strings.ToLower(base) {
	case "interceptor", "interceptor.exe":
		return true
	default:
		return false
	}
}

// rebrandInstallPath maps a legacy interceptor install path to interseptor.
// ok is false when dest is already the current name (or unrelated).
func rebrandInstallPath(dest string) (string, bool) {
	base := filepath.Base(dest)
	switch strings.ToLower(base) {
	case "interceptor":
		return filepath.Join(filepath.Dir(dest), "interseptor"), true
	case "interceptor.exe":
		return filepath.Join(filepath.Dir(dest), "interseptor.exe"), true
	default:
		return dest, false
	}
}

// resolveInstallDest returns where to write the new binary. When the running
// executable is still named interceptor, installDest is interseptor in the same
// directory and legacyPath is the old path (for a compatibility shim).
func resolveInstallDest(currentExe string) (installDest, legacyPath string, rebranded bool) {
	if next, ok := rebrandInstallPath(currentExe); ok {
		return next, currentExe, true
	}
	return currentExe, "", false
}

func printRebrandNotice(out io.Writer, legacyPath, newPath string) {
	if out == nil {
		out = os.Stderr
	}
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "══════════════════════════════════════════════════════════\n")
	fmt.Fprintf(out, "  Renamed: interceptor → interseptor\n")
	fmt.Fprintf(out, "══════════════════════════════════════════════════════════\n")
	fmt.Fprintf(out, "  The product was renamed. This update installed:\n")
	fmt.Fprintf(out, "    %s\n", newPath)
	if legacyPath != "" {
		fmt.Fprintf(out, "  A compatibility shim remains at:\n")
		fmt.Fprintf(out, "    %s\n", legacyPath)
	}
	fmt.Fprintf(out, "  Prefer:  interseptor …\n")
	fmt.Fprintf(out, "  Updates: interseptor update\n")
	fmt.Fprintf(out, "══════════════════════════════════════════════════════════\n")
}

// MaybeRebrandExecutable renames a running legacy "interceptor" binary to
// "interseptor" in place (writes the new name, replaces the old path with a
// shim). Safe to call on every startup — no-op when already named interseptor.
// Returns whether a rebrand happened.
func MaybeRebrandExecutable(out io.Writer) (bool, error) {
	dest, err := os.Executable()
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(dest); err == nil {
		// If we're already executing via a shim → interseptor, don't touch it.
		if !isLegacyBinaryName(filepath.Base(resolved)) && isLegacyBinaryName(filepath.Base(dest)) {
			return false, nil
		}
		dest = resolved
	}
	installDest, legacyPath, rebranded := resolveInstallDest(dest)
	if !rebranded {
		return false, nil
	}
	self, err := os.ReadFile(dest)
	if err != nil {
		return false, err
	}
	if err := installBinary(installDest, self, legacyPath); err != nil {
		return false, err
	}
	printRebrandNotice(out, legacyPath, installDest)
	return true, nil
}

func fetchRelease(ctx context.Context, version string) (*releaseInfo, error) {
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")

	if githubToken() != "" {
		rel, err := fetchReleaseAPI(ctx, tag)
		if err == nil {
			return rel, nil
		}
		if apiErr, ok := err.(releaseAPIError); ok && apiErr.status == http.StatusNotFound {
			return nil, fmt.Errorf("release %s not found", tag)
		}
		if apiErr, ok := err.(releaseAPIError); !ok || !githubAPIRateLimited(apiErr.status) {
			return nil, err
		}
	}

	rel := syntheticRelease(tag)
	if err := verifyReleaseAssets(ctx, rel); err != nil {
		return nil, err
	}
	return rel, nil
}

type releaseAPIError struct {
	status int
	msg    string
}

func (e releaseAPIError) Error() string { return e.msg }

func fetchReleaseAPI(ctx context.Context, tag string) (*releaseInfo, error) {
	u := fmt.Sprintf("%s/releases/tags/%s", githubAPIRoot, tag)
	req, err := newGitHubRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, releaseAPIError{status: resp.StatusCode, msg: fmt.Sprintf("release %s not found", tag)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, releaseAPIError{status: resp.StatusCode, msg: githubAPIError(resp, "github release").Error()}
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	rel := &releaseInfo{Tag: raw.TagName}
	for _, a := range raw.Assets {
		rel.Assets = append(rel.Assets, releaseAsset{Name: a.Name, URL: a.BrowserDownloadURL})
	}
	return rel, nil
}

// syntheticRelease builds release metadata from GoReleaser asset naming without
// calling api.github.com (works when the GitHub API rate limit is exhausted).
func syntheticRelease(tag string) *releaseInfo {
	ver := strings.TrimPrefix(tag, "v")
	var assets []releaseAsset
	for _, name := range assetCandidates(ver, runtime.GOOS, runtime.GOARCH) {
		assets = append(assets, releaseAsset{Name: name, URL: releaseDownloadURL(tag, name)})
	}
	assets = append(assets, releaseAsset{Name: "checksums.txt", URL: releaseDownloadURL(tag, "checksums.txt")})
	return &releaseInfo{Tag: tag, Assets: assets}
}

func verifyReleaseAssets(ctx context.Context, rel *releaseInfo) error {
	ver := strings.TrimPrefix(rel.Tag, "v")
	var verified []releaseAsset
	for _, name := range assetCandidates(ver, runtime.GOOS, runtime.GOARCH) {
		url := releaseDownloadURL(rel.Tag, name)
		if releaseAssetExists(ctx, url) {
			verified = append(verified, releaseAsset{Name: name, URL: url})
		}
	}
	if len(verified) == 0 {
		return fmt.Errorf("release %s not found", rel.Tag)
	}
	verified = append(verified, releaseAsset{
		Name: "checksums.txt",
		URL:  releaseDownloadURL(rel.Tag, "checksums.txt"),
	})
	rel.Assets = verified
	return nil
}

func releaseAssetExists(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "interseptor/"+String()+" (https://github.com/"+Repo+")")
	resp, err := githubWebHTTP.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "interseptor/"+String()+" (https://github.com/"+Repo+")")
	resp, err = githubWebHTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
}

// pickAsset chooses a release archive for the running platform.
func pickAsset(rel *releaseInfo, version string) (name, url string) {
	candidates := assetCandidates(version, runtime.GOOS, runtime.GOARCH)
	byName := map[string]string{}
	for _, a := range rel.Assets {
		byName[strings.ToLower(a.Name)] = a.URL
	}
	for _, c := range candidates {
		if u, ok := byName[strings.ToLower(c)]; ok {
			return c, u
		}
	}
	// Fuzzy: any archive that mentions os+arch.
	osToken, archToken := platformTokens(runtime.GOOS, runtime.GOARCH)
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

// assetCandidates lists the archive names to look for, newest naming first.
// It intentionally also lists the pre-rebrand "interceptor_*"/"interceptor-*"
// names as a fallback: releases published before the product rename used that
// naming, and self-update must keep finding those until a release built under
// the renamed .goreleaser.yaml (binary/project_name: interseptor) is cut.
func assetCandidates(version, goos, goarch string) []string {
	osToken, archToken := platformTokens(goos, goarch)
	v := strings.TrimPrefix(version, "v")
	base := []string{
		fmt.Sprintf("interseptor_%s_%s_%s", v, osToken, archToken),
		fmt.Sprintf("interseptor_%s_%s", osToken, archToken),
		fmt.Sprintf("interseptor-%s-%s-%s", v, osToken, archToken),
		fmt.Sprintf("interseptor-%s-%s", osToken, archToken),
		fmt.Sprintf("interceptor_%s_%s_%s", v, osToken, archToken),
		fmt.Sprintf("interceptor_%s_%s", osToken, archToken),
		fmt.Sprintf("interceptor-%s-%s-%s", v, osToken, archToken),
		fmt.Sprintf("interceptor-%s-%s", osToken, archToken),
	}
	var out []string
	for _, b := range base {
		out = append(out, b+".tar.gz", b+".zip")
	}
	return out
}

func platformTokens(goos, goarch string) (osToken, archToken string) {
	switch goos {
	case "darwin":
		osToken = "darwin"
	case "windows":
		osToken = "windows"
	default:
		osToken = "linux"
	}
	switch goarch {
	case "arm64":
		archToken = "arm64"
	default:
		archToken = "amd64"
	}
	return osToken, archToken
}

func checksumFor(rel *releaseInfo, assetName string) (string, bool) {
	var url string
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, "checksums.txt") {
			url = a.URL
			break
		}
	}
	if url == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := download(ctx, url, nil)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		if strings.EqualFold(parts[len(parts)-1], assetName) || strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), strings.ToLower(assetName)) {
			return parts[0], true
		}
	}
	return "", false
}

func verifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, strings.TrimPrefix(strings.ToLower(want), "sha256:")) {
		return fmt.Errorf("checksum mismatch: got %s", got)
	}
	return nil
}

func download(ctx context.Context, url string, prog *updateProgress) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	var body io.Reader = resp.Body
	if prog != nil {
		body = &progressReader{r: resp.Body, prog: prog, total: resp.ContentLength}
	}
	data, err := io.ReadAll(io.LimitReader(body, 256<<20))
	if prog != nil && prog.term {
		prog.downloadProgress(int64(len(data)), resp.ContentLength)
	}
	return data, err
}

func extractBinary(archive []byte, name string) ([]byte, error) {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".tar.gz"):
		return untarGz(archive)
	case strings.HasSuffix(low, ".zip"):
		return unzipBin(archive)
	default:
		return nil, fmt.Errorf("unsupported archive %q", name)
	}
}

const (
	maxUpdateArchiveEntries = 64
	maxUpdateBinaryBytes    = 128 << 20
	maxUpdateExpandedBytes  = 256 << 20
)

type archiveReadLimits struct {
	entries    int
	fileBytes  int64
	totalBytes int64
}

var defaultUpdateArchiveLimits = archiveReadLimits{
	entries:    maxUpdateArchiveEntries,
	fileBytes:  maxUpdateBinaryBytes,
	totalBytes: maxUpdateExpandedBytes,
}

func untarGz(data []byte) ([]byte, error) {
	return untarGzWithLimits(data, defaultUpdateArchiveLimits)
}

func untarGzWithLimits(data []byte, limits archiveReadLimits) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	entryCount := 0
	var expandedBytes int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entryCount++
		if entryCount > limits.entries {
			return nil, fmt.Errorf("update archive exceeds entry count limit of %d", limits.entries)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Size > limits.fileBytes {
			return nil, fmt.Errorf("update archive member %q exceeds expanded size limit of %d bytes", h.Name, limits.fileBytes)
		}
		if h.Size > limits.totalBytes-expandedBytes {
			return nil, fmt.Errorf("update archive exceeds cumulative expanded size limit of %d bytes", limits.totalBytes)
		}
		expandedBytes += h.Size
		base := filepath.Base(h.Name)
		if isReleaseBinaryName(base) {
			binary, err := io.ReadAll(&io.LimitedReader{R: tr, N: limits.fileBytes + 1})
			if err != nil {
				return nil, fmt.Errorf("read update binary: %w", err)
			}
			if int64(len(binary)) > limits.fileBytes {
				return nil, fmt.Errorf("update binary exceeds expanded size limit of %d bytes", limits.fileBytes)
			}
			return binary, nil
		}
	}
	return nil, fmt.Errorf("interseptor binary not found in archive")
}

// isReleaseBinaryName matches both the renamed binary and the pre-rebrand
// name, so self-update keeps working against archives built before the
// product rename (see assetCandidates).
func isReleaseBinaryName(base string) bool {
	switch base {
	case "interseptor", "interseptor.exe", "interceptor", "interceptor.exe":
		return true
	default:
		return false
	}
}

func unzipBin(data []byte) ([]byte, error) {
	return unzipBinWithLimits(data, defaultUpdateArchiveLimits)
}

func unzipBinWithLimits(data []byte, limits archiveReadLimits) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) > limits.entries {
		return nil, fmt.Errorf("update archive exceeds entry count limit of %d", limits.entries)
	}
	var expandedBytes int64
	for _, f := range zr.File {
		if f.UncompressedSize64 > uint64(limits.fileBytes) {
			return nil, fmt.Errorf("update archive member %q exceeds expanded size limit of %d bytes", f.Name, limits.fileBytes)
		}
		if f.UncompressedSize64 > uint64(limits.totalBytes-expandedBytes) {
			return nil, fmt.Errorf("update archive exceeds cumulative expanded size limit of %d bytes", limits.totalBytes)
		}
		expandedBytes += int64(f.UncompressedSize64)
		base := filepath.Base(f.Name)
		if !isReleaseBinaryName(base) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		binary, err := io.ReadAll(&io.LimitedReader{R: rc, N: limits.fileBytes + 1})
		if err != nil {
			return nil, fmt.Errorf("read update binary: %w", err)
		}
		if int64(len(binary)) > limits.fileBytes {
			return nil, fmt.Errorf("update binary exceeds expanded size limit of %d bytes", limits.fileBytes)
		}
		return binary, nil
	}
	return nil, fmt.Errorf("interseptor binary not found in zip")
}

func installBinary(dest string, data []byte, legacyPath string) error {
	if runtime.GOOS == "windows" {
		return installBinaryWindows(dest, data, legacyPath)
	}
	tmp := dest + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if legacyPath != "" && legacyPath != dest {
		_ = installLegacyShim(legacyPath, dest)
	}
	return nil
}

// installLegacyShim replaces the old interceptor path with a symlink (or a
// tiny shell wrapper) pointing at interseptor, so existing PATH / scripts keep
// working during the rename.
func installLegacyShim(legacyPath, target string) error {
	_ = os.Remove(legacyPath)
	if err := os.Symlink(target, legacyPath); err == nil {
		return nil
	}
	// Symlink failed (e.g. no privilege on some volumes) — drop a wrapper script.
	wrapper := "#!/bin/sh\nexec \"" + target + "\" \"$@\"\n"
	if err := os.WriteFile(legacyPath, []byte(wrapper), 0o755); err != nil {
		return err
	}
	return nil
}

func installBinaryWindows(dest string, data []byte, legacyPath string) error {
	newPath := dest + ".new"
	if err := os.WriteFile(newPath, data, 0o755); err != nil {
		return err
	}
	// Can't replace a running .exe — hand off to a short-lived updater script
	// that waits for locks, stops other interseptor.exe processes, retries the
	// replace, and restarts Interseptor.
	dir := filepath.Dir(dest)
	bat := filepath.Join(dir, "interseptor-update.bat")
	logPath := filepath.Join(dir, "interseptor-update.log")
	script := windowsUpdateScript(newPath, dest, bat, logPath, legacyPath)
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", "start", "/min", "", bat)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start updater — replace %s with %s manually: %w", dest, newPath, err)
	}
	return ErrRestartRequired
}

// windowsUpdateScript builds a cmd batch file that finishes an in-place update.
// legacyPath, when non-empty, is a pre-rename interceptor.exe to replace with a
// .bat shim after the new interseptor.exe is in place.
func windowsUpdateScript(newPath, dest, bat, logPath, legacyPath string) string {
	// Paths are quoted; batch treats % as special so double them in literals.
	legacyBlock := ""
	if legacyPath != "" {
		legacyBat := strings.TrimSuffix(legacyPath, filepath.Ext(legacyPath)) + ".bat"
		if strings.HasSuffix(strings.ToLower(legacyPath), ".exe") {
			// Replace interceptor.exe with interceptor.bat that forwards to interseptor.exe
			legacyBlock = fmt.Sprintf(`
REM Compatibility shim for the pre-rename binary name.
if exist "%%LEGACY%%" del "%%LEGACY%%" >nul 2>&1
echo @echo off> "%s"
echo "%%~dp0interseptor.exe" %%*>> "%s"
`, legacyBat, legacyBat)
		}
	}
	return fmt.Sprintf(`@echo off
setlocal EnableDelayedExpansion
set "NEW=%s"
set "DEST=%s"
set "BAT=%s"
set "LOG=%s"
set "LEGACY=%s"

REM Let the `+"`interseptor update`"+` CLI exit and release its exe handle.
timeout /t 3 /nobreak >nul

REM Stop any still-running Interseptor servers so the exe can be replaced.
for /f "tokens=2" %%%%p in ('tasklist /FI "IMAGENAME eq interseptor.exe" /NH 2^>nul ^| findstr /i "interseptor.exe"') do (
  taskkill /PID %%%%p /T /F >nul 2>&1
)
for /f "tokens=2" %%%%p in ('tasklist /FI "IMAGENAME eq interceptor.exe" /NH 2^>nul ^| findstr /i "interceptor.exe"') do (
  taskkill /PID %%%%p /T /F >nul 2>&1
)
timeout /t 1 /nobreak >nul

set /a TRY=0
:retry
move /Y "%%NEW%%" "%%DEST%%" >nul 2>&1
if not errorlevel 1 goto success
set /a TRY+=1
if !TRY! GEQ 90 goto fail
timeout /t 2 /nobreak >nul
goto retry

:success
if exist "%%NEW%%" del "%%NEW%%" >nul 2>&1
%s
del "%%BAT%%" >nul 2>&1
start "" "%%DEST%%"
exit /b 0

:fail
echo [%%date%% %%time%%] Could not replace %%DEST%% after 90 attempts.>> "%%LOG%%"
echo Staged binary kept at %%NEW%%>> "%%LOG%%"
exit /b 1
`, newPath, dest, bat, logPath, legacyPath, legacyBlock)
}

func goInstall(ctx context.Context, version string, out io.Writer) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found in PATH")
	}
	mod := "github.com/Veyal/interseptor/cmd/interseptor@v" + strings.TrimPrefix(version, "v")
	cmd := exec.CommandContext(ctx, "go", "install", mod)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	return nil
}

func goInstallBin() (string, error) {
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "", err
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return "", fmt.Errorf("empty GOPATH")
	}
	name := "interseptor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(gopath, "bin", name), nil
}
