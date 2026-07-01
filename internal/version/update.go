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
			fmt.Fprintf(out, "interceptor v%s is already up to date\n", String())
			return nil
		}
		if opts.Check {
			cur := String()
			if cur == latest {
				fmt.Fprintf(out, "interceptor v%s is up to date\n", cur)
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
			fmt.Fprintf(out, "interceptor v%s is up to date\n", cur)
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
		fmt.Fprintf(out, "interceptor v%s is already up to date\n", String())
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
		prog.step("Installing to %s…", dest)
		if err := installBinary(dest, bin); err != nil {
			if errors.Is(err, ErrRestartRequired) {
				prog.done("Updater started — quit this interceptor process, then run the new binary")
				return err
			}
			return err
		}
		prog.done("Updated to interceptor v%s → %s", ver, dest)
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
		prog.done("Installed interceptor v%s via go install → %s", ver, bin)
	} else {
		prog.done("Installed interceptor v%s via go install (ensure $(go env GOPATH)/bin is on your PATH)", ver)
	}
	printMCPUpdateNote(out)
	return nil
}

func printMCPUpdateNote(out io.Writer) {
	fmt.Fprintf(out, "\nMCP: if Cursor uses Streamable HTTP (http://127.0.0.1:9966/mcp), restart Interceptor to pick up this build — no MCP config change needed.\n")
	fmt.Fprintf(out, "     stdio clients: restart the MCP server or use scripts/interceptor-mcp to resolve the updated binary on PATH.\n")
}

func fetchRelease(ctx context.Context, version string) (*releaseInfo, error) {
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")
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
		return nil, fmt.Errorf("release %s not found", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, githubAPIError(resp, "github release")
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

func assetCandidates(version, goos, goarch string) []string {
	osToken, archToken := platformTokens(goos, goarch)
	v := strings.TrimPrefix(version, "v")
	base := []string{
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

func untarGz(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		base := filepath.Base(h.Name)
		if base == "interceptor" || base == "interceptor.exe" {
			return io.ReadAll(io.LimitReader(tr, 128<<20))
		}
	}
	return nil, fmt.Errorf("interceptor binary not found in archive")
}

func unzipBin(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != "interceptor" && base != "interceptor.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 128<<20))
	}
	return nil, fmt.Errorf("interceptor binary not found in zip")
}

func installBinary(dest string, data []byte) error {
	if runtime.GOOS == "windows" {
		return installBinaryWindows(dest, data)
	}
	tmp := dest + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func installBinaryWindows(dest string, data []byte) error {
	newPath := dest + ".new"
	if err := os.WriteFile(newPath, data, 0o755); err != nil {
		return err
	}
	// Can't replace a running .exe — hand off to a short-lived updater script.
	bat := filepath.Join(filepath.Dir(dest), "interceptor-update.bat")
	script := fmt.Sprintf(`@echo off
timeout /t 1 /nobreak >nul
move /y "%s" "%s"
del "%s"
`, newPath, dest, bat)
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", "start", "/min", "", bat)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start updater — replace %s with %s manually: %w", dest, newPath, err)
	}
	return ErrRestartRequired
}

func goInstall(ctx context.Context, version string, out io.Writer) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found in PATH")
	}
	mod := "github.com/Veyal/interceptor/cmd/interceptor@v" + strings.TrimPrefix(version, "v")
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
	name := "interceptor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(gopath, "bin", name), nil
}
