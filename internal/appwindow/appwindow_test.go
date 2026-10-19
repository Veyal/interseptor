package appwindow

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type started struct {
	name string
	args []string
}

func fakeLauncher(goos string, env map[string]string, bin string, binErr error) (*Launcher, *[]started) {
	var calls []started
	l := &Launcher{
		getenv:  func(k string) string { return env[k] },
		goos:    goos,
		browser: func() (string, error) { return bin, binErr },
		start: func(name string, args []string) error {
			calls = append(calls, started{name, args})
			return nil
		},
	}
	return l, &calls
}

func TestAppArgs(t *testing.T) {
	got := AppArgs("http://127.0.0.1:9966", "/tmp/profile")
	want := []string{
		"--app=http://127.0.0.1:9966",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppArgs = %q, want %q", got, want)
	}
}

func TestOpenDarwinBundleUsesOpenNewInstance(t *testing.T) {
	l, calls := fakeLauncher("darwin", nil, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", nil)
	if err := l.Open("http://127.0.0.1:9966", "/p"); err != nil {
		t.Fatal(err)
	}
	want := started{"open", append([]string{"-n", "-a", "/Applications/Google Chrome.app", "--args"}, AppArgs("http://127.0.0.1:9966", "/p")...)}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("started %+v, want %+v", *calls, want)
	}
}

func TestOpenDarwinBareBinaryRunsDirectly(t *testing.T) {
	l, calls := fakeLauncher("darwin", nil, "/usr/local/bin/chromium", nil)
	if err := l.Open("http://x", "/p"); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].name; got != "/usr/local/bin/chromium" {
		t.Fatalf("started %q, want the binary itself", got)
	}
}

func TestOpenWindowsRunsBrowserDirectly(t *testing.T) {
	bin := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	l, calls := fakeLauncher("windows", nil, bin, nil)
	if err := l.Open("http://x", `C:\p`); err != nil {
		t.Fatal(err)
	}
	want := started{bin, AppArgs("http://x", `C:\p`)}
	if !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("started %+v, want %+v", (*calls)[0], want)
	}
}

func TestOpenLinuxNeedsDisplay(t *testing.T) {
	l, calls := fakeLauncher("linux", nil, "/usr/bin/chromium", nil)
	err := l.Open("http://x", "/p")
	if !errors.Is(err, ErrNoDisplay) {
		t.Fatalf("err = %v, want ErrNoDisplay", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("started a browser without a display: %+v", *calls)
	}
	for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
		l, calls := fakeLauncher("linux", map[string]string{k: ":0"}, "/usr/bin/chromium", nil)
		if err := l.Open("http://x", "/p"); err != nil || len(*calls) != 1 {
			t.Fatalf("%s set: err=%v calls=%d, want one launch", k, err, len(*calls))
		}
	}
}

func TestOpenReportsMissingBrowser(t *testing.T) {
	l, calls := fakeLauncher("darwin", nil, "", errors.New("no Chrome"))
	if err := l.Open("http://x", "/p"); err == nil || !strings.Contains(err.Error(), "no Chrome") {
		t.Fatalf("err = %v, want the locator error", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("started %+v with no browser", *calls)
	}
}

func TestOpenReportsLaunchFailure(t *testing.T) {
	l, _ := fakeLauncher("windows", nil, `C:\chrome.exe`, nil)
	l.start = func(string, []string) error { return errors.New("exit status 1") }
	if err := l.Open("http://x", "/p"); err == nil {
		t.Fatal("want the launch error")
	}
}

func TestProfileDir(t *testing.T) {
	if d := ProfileDir(); !strings.Contains(d, "Interseptor") {
		t.Fatalf("ProfileDir = %q, want an Interseptor-owned directory", d)
	}
}

// Review regression: the browser profile holds the control session, so it must be
// a private directory the current user owns, never a pre-planted shared one.
func TestEnsurePrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("profile dir mode %v is readable by others", fi.Mode().Perm())
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensurePrivateDir(dir); err != nil {
			t.Fatalf("an own dir with wide permissions is tightened, not refused: %v", err)
		}
		if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("permissions not tightened: %v", fi.Mode().Perm())
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		if err := ensurePrivateDir(link); err == nil {
			t.Fatal("a symlinked profile dir must be refused")
		}
	}
}

func TestOpenRefusesUnsafeProfileDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	l, calls := fakeLauncher("darwin", nil, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", nil)
	l.prepare = ensurePrivateDir
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := l.Open("http://x", link); err == nil || len(*calls) != 0 {
		t.Fatalf("Open must refuse a symlinked profile dir without launching: err=%v calls=%d", err, len(*calls))
	}
}
