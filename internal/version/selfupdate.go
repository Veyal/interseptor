package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InstallMethod names how the running binary was installed. It decides whether
// a running server may replace its own executable (the web UI's one-click
// update) or must point the operator at the owning package manager.
type InstallMethod string

const (
	InstallSelf     InstallMethod = "self"     // a standalone binary this process may replace
	InstallBrew     InstallMethod = "brew"     // owned by Homebrew
	InstallScoop    InstallMethod = "scoop"    // owned by Scoop
	InstallDesktop  InstallMethod = "desktop"  // inside the macOS app bundle; the native app updates itself
	InstallReadonly InstallMethod = "readonly" // the executable's directory is not writable
	InstallDev      InstallMethod = "dev"      // a local/dev build, not a release
)

// InstallInfo is the result of DetectInstall. Command is the shell command to
// show for package-manager installs; Reason explains every refusal.
type InstallInfo struct {
	Method  InstallMethod
	Command string
	Reason  string
}

// SelfUpdatable reports whether the binary may be replaced in place.
func (i InstallInfo) SelfUpdatable() bool { return i.Method == InstallSelf }

// DetectInstall classifies an install from the invoked executable path, its
// symlink-resolved target (empty when unknown), and the running version.
// writable probes whether a directory accepts new files. Both paths are
// checked because a Homebrew or app-bundle binary is usually reached through a
// symlink in a bin directory.
func DetectInstall(exe, resolved, ver string, writable func(dir string) bool) InstallInfo {
	paths := []string{normPath(exe)}
	if resolved != "" && resolved != exe {
		paths = append(paths, normPath(resolved))
	}
	any := func(match func(string) bool) bool {
		for _, p := range paths {
			if match(p) {
				return true
			}
		}
		return false
	}
	switch {
	case any(func(p string) bool { return strings.Contains(p, ".app/contents/macos/") }):
		return InstallInfo{Method: InstallDesktop, Reason: "Interseptor is running inside the macOS app, which installs its own updates."}
	case any(isBrewPath):
		return InstallInfo{Method: InstallBrew, Command: "brew upgrade interseptor", Reason: "This copy is managed by Homebrew."}
	case any(func(p string) bool { return strings.Contains(p, "/scoop/apps/") }):
		return InstallInfo{Method: InstallScoop, Command: "scoop update interseptor", Reason: "This copy is managed by Scoop."}
	case isDevVersion(ver):
		return InstallInfo{Method: InstallDev, Reason: "This is a local development build; rebuild from source or install a release."}
	}
	target := resolved
	if target == "" {
		target = exe
	}
	if writable != nil && !writable(filepath.Dir(target)) {
		return InstallInfo{Method: InstallReadonly, Reason: "The folder holding the Interseptor binary is not writable by this user; update it with the tool that installed it, or run `interseptor update` with sufficient permissions."}
	}
	return InstallInfo{Method: InstallSelf}
}

func normPath(p string) string { return strings.ToLower(strings.ReplaceAll(p, `\`, "/")) }

func isBrewPath(p string) bool {
	return strings.Contains(p, "/cellar/") || strings.HasPrefix(p, "/opt/homebrew/") || strings.Contains(p, "/.linuxbrew/")
}

func isDevVersion(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "dev" || v == "(devel)" || strings.HasSuffix(v, "-local") || strings.HasSuffix(v, "-dev")
}

// DirWritable reports whether a new file can be created in dir. The probe file
// is removed immediately.
func DirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".interseptor-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// IsNewer reports whether latest should replace cur (semantic comparison; a
// prerelease is older than the same core release).
func IsNewer(cur, latest string) bool { return latestIsNewer(cur, latest) }

// ReleaseNotes is the human-facing part of a GitHub release.
type ReleaseNotes struct {
	Version string
	Body    string // markdown
	URL     string // release page
}

// ReleasePageURL is the GitHub page for a release tag.
func ReleasePageURL(ver string) string {
	return fmt.Sprintf("https://github.com/%s/releases/tag/v%s", Repo, strings.TrimPrefix(ver, "v"))
}

// FetchReleaseNotes reads the body and page URL of a release. It works without
// a token (one unauthenticated API call); on failure the returned notes still
// carry the release page URL so callers can link to it.
func FetchReleaseNotes(ctx context.Context, ver string) (ReleaseNotes, error) {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	notes := ReleaseNotes{Version: ver, URL: ReleasePageURL(ver)}
	req, err := newGitHubRequest(ctx, http.MethodGet, fmt.Sprintf("%s/releases/tags/v%s", githubAPIRoot, ver))
	if err != nil {
		return notes, err
	}
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return notes, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return notes, githubAPIError(resp, "github release notes")
	}
	var raw struct {
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return notes, err
	}
	notes.Body = raw.Body
	if strings.HasPrefix(raw.HTMLURL, "https://github.com/") {
		notes.URL = raw.HTMLURL
	}
	return notes, nil
}

// Stage phases reported through StageOptions.Progress.
const (
	PhaseDownloading = "downloading"
	PhaseVerifying   = "verifying"
	PhaseInstalling  = "installing"
)

// StageProgress is one progress report. Done/Total are archive bytes; Total is
// 0 when the server sent no length.
type StageProgress struct {
	Phase       string
	Done, Total int64
}

// StageOptions configures Stage. Exe must be the symlink-resolved path of the
// binary to replace (callers inject it so tests never touch a real install).
// GOOS selects the replace strategy (default: the running OS).
type StageOptions struct {
	Version  string
	Exe      string
	GOOS     string
	Progress func(StageProgress)
}

// StagedUpdate describes a verified new binary. Dest is the executable to run
// after the restart. Staged is non-empty only when the binary could not be
// replaced in place (Windows): FinishStaged moves it over Dest.
type StagedUpdate struct {
	Version string
	Dest    string
	Staged  string
}

// Stage downloads the release archive for this platform, verifies it against
// the release's checksums.txt (fail closed), extracts the binary and installs
// it. On Unix the executable is replaced by an atomic rename, which a running
// process survives (it keeps the old inode). On Windows a running .exe cannot
// be overwritten, so the binary is written next to it as <exe>.new and
// FinishStaged swaps it in right before the restart.
func Stage(ctx context.Context, opts StageOptions) (StagedUpdate, error) {
	staged, err := stage(ctx, opts)
	if err != nil {
		if ce := ctx.Err(); ce != nil {
			return StagedUpdate{}, ce
		}
		return StagedUpdate{}, err
	}
	return staged, nil
}

func stage(ctx context.Context, opts StageOptions) (StagedUpdate, error) {
	report := opts.Progress
	if report == nil {
		report = func(StageProgress) {}
	}
	if err := ctx.Err(); err != nil {
		return StagedUpdate{}, err
	}
	if opts.Exe == "" {
		return StagedUpdate{}, errors.New("executable path required")
	}
	ver := strings.TrimPrefix(strings.TrimSpace(opts.Version), "v")
	rel, err := fetchRelease(ctx, ver)
	if err != nil {
		return StagedUpdate{}, err
	}
	name, url := pickAsset(rel, ver)
	if url == "" {
		return StagedUpdate{}, fmt.Errorf("release v%s has no prebuilt binary for this platform", ver)
	}
	var total int64
	report(StageProgress{Phase: PhaseDownloading})
	data, err := downloadReport(ctx, url, func(done, size int64) {
		total = size
		report(StageProgress{Phase: PhaseDownloading, Done: done, Total: size})
	})
	if err != nil {
		return StagedUpdate{}, err
	}
	if total <= 0 {
		total = int64(len(data))
	}
	size := int64(len(data))
	report(StageProgress{Phase: PhaseVerifying, Done: size, Total: total})
	sum, err := checksumForCtx(ctx, rel, name)
	if err != nil {
		return StagedUpdate{}, fmt.Errorf("refusing to install %s: %w", name, err)
	}
	if err := verifySHA256(data, sum); err != nil {
		return StagedUpdate{}, err
	}
	bin, err := extractBinary(data, name)
	if err != nil {
		return StagedUpdate{}, err
	}
	if err := ctx.Err(); err != nil {
		return StagedUpdate{}, err
	}
	report(StageProgress{Phase: PhaseInstalling, Done: size, Total: total})
	return placeBinary(opts, ver, bin)
}

func placeBinary(opts StageOptions, ver string, bin []byte) (StagedUpdate, error) {
	dest, legacy, _ := resolveInstallDest(opts.Exe)
	if opts.GOOS == "windows" {
		next := dest + ".new"
		if err := os.WriteFile(next, bin, 0o755); err != nil {
			return StagedUpdate{}, err
		}
		return StagedUpdate{Version: ver, Dest: dest, Staged: next}, nil
	}
	if err := installBinaryUnix(dest, bin, legacy); err != nil {
		return StagedUpdate{}, err
	}
	return StagedUpdate{Version: ver, Dest: dest}, nil
}

// FinishStaged completes a staged (Windows) update: the running executable is
// renamed to <exe>.old — Windows allows renaming, not overwriting, a running
// image — and the staged binary takes its name. On failure the original is put
// back. A no-op when the update was already installed in place.
func FinishStaged(s StagedUpdate) error {
	if s.Staged == "" {
		return nil
	}
	old := s.Dest + ".old"
	parked := false
	if _, err := os.Stat(s.Dest); err == nil {
		_ = os.Remove(old)
		if err := os.Rename(s.Dest, old); err != nil {
			return fmt.Errorf("move running binary aside: %w", err)
		}
		parked = true
	}
	if err := os.Rename(s.Staged, s.Dest); err != nil {
		if parked {
			_ = os.Rename(old, s.Dest)
		}
		return fmt.Errorf("install staged binary: %w", err)
	}
	return nil
}

// CleanupReplacedBinary removes the <exe>.old left by FinishStaged once the new
// process runs. Best-effort: on Windows the exiting parent can still hold the
// file for a moment, so it retries in the background for a few seconds.
func CleanupReplacedBinary(exe string) {
	if exe == "" {
		return
	}
	old := exe + ".old"
	if err := os.Remove(old); err == nil || os.IsNotExist(err) {
		return
	}
	go func() {
		for i := 0; i < 20; i++ {
			time.Sleep(500 * time.Millisecond)
			if err := os.Remove(old); err == nil || os.IsNotExist(err) {
				return
			}
		}
	}()
}

// Updater is the self-update surface the control plane drives, bound to one
// executable. Exe is the invoked path; Resolved its symlink target.
type Updater struct {
	Exe      string
	Resolved string
	GOOS     string
}

// NewUpdater binds an Updater to exe (normally os.Executable()).
func NewUpdater(exe, goos string) *Updater {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return &Updater{Exe: exe, Resolved: resolved, GOOS: goos}
}

// Install classifies how this binary was installed.
func (u *Updater) Install() InstallInfo {
	return sourceBuildGuard(DetectInstall(u.Exe, u.Resolved, String(), DirWritable), Channel)
}

// sourceBuildGuard reclassifies a replaceable binary that is not a published
// release build as a development build. A plain `go build` carries the same
// fallback version and Go pseudo-version as a release, so only the release
// channel stamp tells them apart.
func sourceBuildGuard(info InstallInfo, channel string) InstallInfo {
	if channel == "release" || (info.Method != InstallSelf && info.Method != InstallReadonly) {
		return info
	}
	return InstallInfo{Method: InstallDev, Reason: "This binary was built from source, not a published release; rebuild it or install a release to update."}
}

// Latest asks GitHub for the newest release, bypassing the hourly cache.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	latest, _, err := CheckLatestFresh(ctx)
	return latest, err
}

// Notes returns the release notes for ver.
func (u *Updater) Notes(ctx context.Context, ver string) (ReleaseNotes, error) {
	return FetchReleaseNotes(ctx, ver)
}

// Stage downloads, verifies and installs ver over the resolved executable.
func (u *Updater) Stage(ctx context.Context, ver string, progress func(StageProgress)) (StagedUpdate, error) {
	return Stage(ctx, StageOptions{Version: ver, Exe: u.Resolved, GOOS: u.GOOS, Progress: progress})
}
