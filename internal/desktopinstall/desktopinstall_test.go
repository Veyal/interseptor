package desktopinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func plistXML(id, ver string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>Interseptor</string>
	<key>CFBundleIdentifier</key>
	<string>` + id + `</string>
	<key>LSArchitecturePriority</key>
	<array><string>arm64</string></array>
	<key>CFBundleShortVersionString</key>
	<string>` + ver + `</string>
</dict>
</plist>
`
}

// appZip builds a minimal Interseptor.app zip the way `ditto -c -k --keepParent` lays it out.
func appZip(t *testing.T, id, ver string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"Interseptor.app/Contents/Info.plist":        plistXML(id, ver),
		"Interseptor.app/Contents/MacOS/Interseptor": "#!/bin/sh\n",
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeApp creates an installed app directly on disk.
func writeApp(t *testing.T, dir, id, ver string) string {
	t.Helper()
	app := filepath.Join(dir, AppName)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plistXML(id, ver)), 0o644); err != nil {
		t.Fatal(err)
	}
	return app
}

func unzipTo(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p := filepath.Join(dest, f.Name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if err := os.WriteFile(p, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

type fakeRelease struct {
	srv       *httptest.Server
	zip       []byte
	checksums string
	zipHits   atomic.Int32
}

func newFakeRelease(t *testing.T, ver string, zipBody []byte) *fakeRelease {
	t.Helper()
	fr := &fakeRelease{zip: zipBody}
	sum := sha256.Sum256(zipBody)
	fr.checksums = hex.EncodeToString(sum[:]) + "  " + ZipName("v"+ver) + "\n"
	fr.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+ChecksumsName):
			_, _ = io.WriteString(w, fr.checksums)
		case strings.HasSuffix(r.URL.Path, "-unsigned.zip"):
			fr.zipHits.Add(1)
			w.Header().Set("Content-Length", fmt.Sprint(len(fr.zip)))
			_, _ = w.Write(fr.zip)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

type runLog struct {
	calls     []string
	codesign  int
	running   bool
	dittoFail bool
}

func (l *runLog) run(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	l.calls = append(l.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "ditto":
		if l.dittoFail {
			return []byte("ditto: bad zip"), 1, nil
		}
		if err := unzipTo(args[2], args[3]); err != nil {
			return []byte(err.Error()), 1, nil
		}
		return nil, 0, nil
	case "codesign":
		if l.codesign != 0 {
			return []byte("code object is not signed at all"), l.codesign, nil
		}
		return nil, 0, nil
	case "pgrep":
		if l.running {
			return []byte("4242\n"), 0, nil
		}
		return nil, 1, nil
	}
	return nil, 0, nil
}

func (l *runLog) ran(tool string) bool {
	for _, c := range l.calls {
		if strings.HasPrefix(c, tool+" ") {
			return true
		}
	}
	return false
}

func newTestInstaller(t *testing.T, fr *fakeRelease, rl *runLog, latest string) (*Installer, *bytes.Buffer) {
	t.Helper()
	u, _ := url.Parse(fr.srv.URL)
	out := &bytes.Buffer{}
	in := &Installer{
		GOOS:        "darwin",
		Client:      fr.srv.Client(),
		Latest:      func(context.Context) (string, error) { return latest, nil },
		AssetURL:    func(tag, name string) string { return fr.srv.URL + "/download/" + tag + "/" + name },
		TrustedHost: func(h string) bool { return h == u.Host },
		Run:         rl.run,
		SystemDir:   filepath.Join(t.TempDir(), "Applications"),
		HomeDir:     func() (string, error) { return t.TempDir(), nil },
		Writable:    func(string) bool { return true },
		Out:         out,
	}
	return in, out
}

func installedVersion(t *testing.T, app string) string {
	t.Helper()
	info, err := readInfo(context.Background(), nil, app)
	if err != nil {
		t.Fatalf("read installed app: %v", err)
	}
	return info.Version
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != AppName {
			t.Fatalf("leftover in target dir: %s", e.Name())
		}
	}
}

func TestInstallRefusesNonMacOS(t *testing.T) {
	in := &Installer{GOOS: "linux", Out: io.Discard}
	_, err := in.Install(context.Background(), Options{Dir: t.TempDir()})
	if !errors.Is(err, ErrUnsupportedOS) {
		t.Fatalf("err = %v, want ErrUnsupportedOS", err)
	}
	if !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("message should mention macOS: %v", err)
	}
}

func TestInstallRefusesRelativeDir(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	if _, err := in.Install(context.Background(), Options{Dir: "Applications"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path refusal, got %v", err)
	}
}

func TestInstallSuccess(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	rl := &runLog{}
	in, out := newTestInstaller(t, fr, rl, "2.19.0")
	dir := t.TempDir()
	res, err := in.Install(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatalf("Install: %v\n%s", err, out)
	}
	want := filepath.Join(dir, AppName)
	if res.Path != want || res.Version != "2.19.0" || !res.Installed {
		t.Fatalf("result = %+v", res)
	}
	if got := installedVersion(t, want); got != "2.19.0" {
		t.Fatalf("installed version %q", got)
	}
	for _, tool := range []string{"ditto", "codesign", "xattr"} {
		if !rl.ran(tool) {
			t.Fatalf("%s was not run; calls=%v", tool, rl.calls)
		}
	}
	assertNoLeftovers(t, dir)
	if !strings.Contains(out.String(), "100%") {
		t.Fatalf("expected download progress in output:\n%s", out)
	}
}

func TestInstallUsesRequestedVersion(t *testing.T) {
	fr := newFakeRelease(t, "2.18.0", appZip(t, BundleID, "2.18.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	res, err := in.Install(context.Background(), Options{Dir: dir, Version: "v2.18.0"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Version != "2.18.0" {
		t.Fatalf("version %q", res.Version)
	}
}

func TestInstallChecksumMismatchInstallsNothing(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	fr.checksums = strings.Repeat("0", 64) + "  " + ZipName("v2.19.0") + "\n"
	rl := &runLog{}
	in, _ := newTestInstaller(t, fr, rl, "2.19.0")
	dir := t.TempDir()
	_, err := in.Install(context.Background(), Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if rl.ran("ditto") {
		t.Fatal("ditto must not run on a checksum mismatch")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("target dir not empty: %v", entries)
	}
}

func TestInstallMissingChecksumLineFailsClosed(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	fr.checksums = strings.Repeat("a", 64) + "  x-" + ZipName("v2.19.0") + "\n"
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	_, err := in.Install(context.Background(), Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("expected missing-entry error, got %v", err)
	}
	if fr.zipHits.Load() != 0 {
		t.Fatal("zip must not be downloaded without a checksum")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("target dir not empty: %v", entries)
	}
}

func TestInstallRejectsBadBundle(t *testing.T) {
	cases := []struct {
		name     string
		id, ver  string
		codesign int
		want     string
	}{
		{"wrong bundle id", "com.example.other", "2.19.0", 0, "identifier"},
		{"wrong version", BundleID, "2.18.0", 0, "version"},
		{"codesign failure", BundleID, "2.19.0", 1, "code signature"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fr := newFakeRelease(t, "2.19.0", appZip(t, c.id, c.ver))
			in, _ := newTestInstaller(t, fr, &runLog{codesign: c.codesign}, "2.19.0")
			dir := t.TempDir()
			old := writeApp(t, dir, BundleID, "2.17.0")
			_, err := in.Install(context.Background(), Options{Dir: dir})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q error, got %v", c.want, err)
			}
			if got := installedVersion(t, old); got != "2.17.0" {
				t.Fatalf("existing app changed to %q", got)
			}
			assertNoLeftovers(t, dir)
		})
	}
}

func TestInstallAlreadyInstalledSameVersionSkipsDownload(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	rl := &runLog{}
	in, out := newTestInstaller(t, fr, rl, "2.19.0")
	dir := t.TempDir()
	writeApp(t, dir, BundleID, "2.19.0")
	res, err := in.Install(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Installed {
		t.Fatal("expected Installed=false for an up-to-date app")
	}
	if fr.zipHits.Load() != 0 || rl.ran("ditto") {
		t.Fatal("no download/extract expected")
	}
	if !strings.Contains(out.String(), "already installed") {
		t.Fatalf("output: %s", out)
	}
}

func TestInstallForceReinstallsSameVersion(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	writeApp(t, dir, BundleID, "2.19.0")
	res, err := in.Install(context.Background(), Options{Dir: dir, Force: true})
	if err != nil || !res.Installed || fr.zipHits.Load() != 1 {
		t.Fatalf("res=%+v err=%v hits=%d", res, err, fr.zipHits.Load())
	}
}

func TestInstallRefusesDowngradeWithoutForce(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	app := writeApp(t, dir, BundleID, "2.20.0")
	_, err := in.Install(context.Background(), Options{Dir: dir})
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("err = %v, want ErrDowngrade", err)
	}
	if fr.zipHits.Load() != 0 {
		t.Fatal("no download expected")
	}
	if _, err := in.Install(context.Background(), Options{Dir: dir, Force: true}); err != nil {
		t.Fatalf("forced downgrade: %v", err)
	}
	if got := installedVersion(t, app); got != "2.19.0" {
		t.Fatalf("version after forced downgrade %q", got)
	}
}

func TestInstallRefusesWhileAppRunning(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	rl := &runLog{running: true}
	in, _ := newTestInstaller(t, fr, rl, "2.19.0")
	dir := t.TempDir()
	app := writeApp(t, dir, BundleID, "2.18.0")
	_, err := in.Install(context.Background(), Options{Dir: dir, Force: true})
	if !errors.Is(err, ErrAppRunning) {
		t.Fatalf("err = %v, want ErrAppRunning", err)
	}
	if !strings.Contains(err.Error(), "Quit") {
		t.Fatalf("message should tell the user to quit: %v", err)
	}
	var pattern string
	for _, c := range rl.calls {
		if strings.HasPrefix(c, "pgrep ") {
			pattern = c
		}
	}
	if !strings.Contains(pattern, "-f") || !strings.Contains(pattern, "Contents/MacOS/") {
		t.Fatalf("pgrep call = %q", pattern)
	}
	if got := installedVersion(t, app); got != "2.18.0" {
		t.Fatalf("app changed: %q", got)
	}
}

func TestInstallSwapFailureRestoresOldApp(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	app := writeApp(t, dir, BundleID, "2.18.0")
	in.Rename = func(oldPath, newPath string) error {
		if newPath == app && !strings.Contains(oldPath, "previous") {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldPath, newPath)
	}
	_, err := in.Install(context.Background(), Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "simulated") {
		t.Fatalf("expected swap failure, got %v", err)
	}
	if got := installedVersion(t, app); got != "2.18.0" {
		t.Fatalf("old app not restored: %q", got)
	}
	assertNoLeftovers(t, dir)
}

func TestInstallRejectsUntrustedHosts(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	in.TrustedHost = func(string) bool { return false }
	if _, err := in.Install(context.Background(), Options{Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("expected untrusted-host error, got %v", err)
	}
	in.TrustedHost = nil
	in.AssetURL = func(tag, name string) string { return "http://github.com/x/" + name }
	if _, err := in.Install(context.Background(), Options{Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("expected http refusal, got %v", err)
	}
}

func TestTargetDirFallsBackToHomeApplications(t *testing.T) {
	home := t.TempDir()
	in := &Installer{
		SystemDir: "/Applications",
		HomeDir:   func() (string, error) { return home, nil },
		Writable:  func(string) bool { return false },
	}
	dir, err := in.targetDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Applications")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("~/Applications not created: %v", err)
	}
	in.Writable = func(string) bool { return true }
	if dir, _ := in.targetDir(""); dir != "/Applications" {
		t.Fatalf("writable system dir not preferred: %q", dir)
	}
}

func TestTrustedDownloadHost(t *testing.T) {
	for _, h := range []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"} {
		if !TrustedDownloadHost(h) {
			t.Errorf("%s should be trusted", h)
		}
	}
	for _, h := range []string{"example.com", "github.com.evil.example", "api.github.com"} {
		if TrustedDownloadHost(h) {
			t.Errorf("%s should not be trusted", h)
		}
	}
}

// TestLiveInstall installs the real latest release into a temp directory.
// Opt in with INTERSEPTOR_LIVE_DESKTOP_INSTALL=1 (macOS only, needs network).
func TestLiveInstall(t *testing.T) {
	if os.Getenv("INTERSEPTOR_LIVE_DESKTOP_INSTALL") != "1" {
		t.Skip("set INTERSEPTOR_LIVE_DESKTOP_INSTALL=1 to install the real release into a temp dir")
	}
	in := New(os.Stdout)
	if in.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	dir := t.TempDir()
	res, err := in.Install(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatalf("live install: %v", err)
	}
	t.Logf("installed v%s at %s", res.Version, res.Path)
	assertNoLeftovers(t, dir)
}

// The version becomes part of the release URL and the zip file name.
func TestInstallRejectsCraftedVersion(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	for _, v := range []string{"2.19.0-/../x", "2.19.0?x", "../../2.19.0", "2.19", "2.19.0 x"} {
		if _, err := in.Install(context.Background(), Options{Dir: t.TempDir(), Version: v}); err == nil || !strings.Contains(err.Error(), "invalid release version") {
			t.Errorf("version %q: err = %v, want invalid release version", v, err)
		}
	}
}

// Something at the target that is not a readable app bundle is not replaced
// (and deleted) without --force.
func TestInstallRefusesUnreadableTargetWithoutForce(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	in, _ := newTestInstaller(t, fr, &runLog{}, "2.19.0")
	dir := t.TempDir()
	target := filepath.Join(dir, AppName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(context.Background(), Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "not a readable app bundle") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatalf("the existing item was removed: %v", err)
	}
}

// pgrep matches case-insensitively (APFS) and covers the resolved path too.
func TestRunningCheckIsCaseInsensitive(t *testing.T) {
	fr := newFakeRelease(t, "2.19.0", appZip(t, BundleID, "2.19.0"))
	rl := &runLog{}
	in, _ := newTestInstaller(t, fr, rl, "2.19.0")
	if _, err := in.Install(context.Background(), Options{Dir: t.TempDir()}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, c := range rl.calls {
		if strings.HasPrefix(c, "pgrep ") && !strings.Contains(c, " -i ") {
			t.Fatalf("pgrep must match case-insensitively: %q", c)
		}
	}
}
