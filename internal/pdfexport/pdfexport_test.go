package pdfexport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakePDF = "%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"

// fakeLaunch records the command and runs behave, which stands in for whatever
// the browser does to the filesystem. exits controls whether the browser process
// is reported as having ended.
type fakeLaunch struct {
	args    []string
	logPath string
	stopped int
	behave  func(out, logPath string)
	exit    error
	exits   bool
}

func (f *fakeLaunch) launcher() launcher {
	return func(_ context.Context, _ string, args []string, logPath string) (<-chan error, func(), error) {
		f.args, f.logPath = args, logPath
		if f.behave != nil {
			f.behave(argValue(args, "--print-to-pdf="), logPath)
		}
		exited := make(chan error, 1)
		if f.exits {
			exited <- f.exit
			close(exited)
		}
		return exited, func() { f.stopped++ }, nil
	}
}

// newFake returns a Renderer with a resolvable browser and a fake launcher.
func newFake(t *testing.T, f *fakeLaunch) *Renderer {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := New()
	r.getenv = func(string) string { return "" }
	r.lookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
	r.candidates = []string{bin}
	r.launch = f.launcher()
	r.poll = time.Millisecond
	r.Timeout = 2 * time.Second
	return r
}

func writeFakePDF(out, _ string) {
	if out != "" {
		_ = os.WriteFile(out, []byte(fakePDF), 0o600)
	}
}

func argValue(args []string, prefix string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}
	return ""
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestRenderProducesPDFBytes(t *testing.T) {
	f := &fakeLaunch{behave: writeFakePDF}
	r := newFake(t, f)
	out, err := r.Render(context.Background(), []byte("<!DOCTYPE html><html><body>report</body></html>"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != fakePDF {
		t.Fatalf("wrong bytes: %q", out)
	}
	for _, want := range []string{"--headless=new", "--no-pdf-header-footer", "--disable-background-networking"} {
		if !hasArg(f.args, want) {
			t.Errorf("missing flag %q in %v", want, f.args)
		}
	}
	if hasArg(f.args, "--no-sandbox") {
		t.Error("--no-sandbox must not be passed unless explicitly enabled")
	}
	if profile := argValue(f.args, "--user-data-dir="); profile == "" {
		t.Error("the print must use a throwaway profile, not the operator's")
	}
	if f.stopped == 0 {
		t.Error("the browser was left running")
	}
}

// Chrome does not exit after --print-to-pdf. A render must still finish.
func TestRenderDoesNotWaitForTheBrowserToExit(t *testing.T) {
	f := &fakeLaunch{behave: writeFakePDF, exits: false}
	r := newFake(t, f)
	r.Timeout = 30 * time.Second // would hang if the exit were awaited
	done := make(chan error, 1)
	go func() {
		_, err := r.Render(context.Background(), []byte("<html></html>"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Render waited for a browser that never exits")
	}
}

func TestRenderPassesTheExactHTMLToTheBrowser(t *testing.T) {
	var seen string
	f := &fakeLaunch{}
	f.behave = func(out, logPath string) {
		if b, err := os.ReadFile(argValue(f.args, "file://")); err == nil {
			seen = string(b)
		}
		writeFakePDF(out, logPath)
	}
	r := newFake(t, f)
	html := "<!DOCTYPE html><html><body>unique-marker-9f3a</body></html>"
	if _, err := r.Render(context.Background(), []byte(html)); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if seen != html {
		t.Fatalf("browser was given different bytes:\n%q", seen)
	}
}

func TestRenderWaitsForAnIncompletePDFToFinish(t *testing.T) {
	f := &fakeLaunch{}
	f.behave = func(out, _ string) {
		// First the header only, then the whole file: a half-written PDF must
		// never be returned as the deliverable.
		_ = os.WriteFile(out, []byte("%PDF-1.4\n1 0 obj"), 0o600)
		go func() {
			time.Sleep(60 * time.Millisecond)
			_ = os.WriteFile(out, []byte(fakePDF), 0o600)
		}()
	}
	r := newFake(t, f)
	out, err := r.Render(context.Background(), []byte("<html></html>"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != fakePDF {
		t.Fatalf("returned a partial PDF: %q", out)
	}
}

func TestRenderRejectsOutputThatIsNotAPDF(t *testing.T) {
	f := &fakeLaunch{behave: func(out, _ string) {
		_ = os.WriteFile(out, []byte("<html>chrome wrote an error page</html>"), 0o600)
	}}
	r := newFake(t, f)
	out, err := r.Render(context.Background(), []byte("<html></html>"))
	if err == nil {
		t.Fatal("want an error, got a half-rendered file")
	}
	if out != nil {
		t.Fatalf("bytes returned alongside the error: %q", out)
	}
	if !strings.Contains(err.Error(), "not a PDF") {
		t.Fatalf("error does not say what went wrong: %v", err)
	}
}

func TestRenderReportsABrowserThatExitedWithNothing(t *testing.T) {
	f := &fakeLaunch{exits: true}
	r := newFake(t, f)
	_, err := r.Render(context.Background(), []byte("<html></html>"))
	if err == nil {
		t.Fatal("a browser that wrote nothing must be an error")
	}
	if !strings.Contains(err.Error(), "exited without writing a complete PDF") {
		t.Fatalf("unclear error: %v", err)
	}
}

func TestRenderSurfacesBrowserFailure(t *testing.T) {
	f := &fakeLaunch{
		exits: true, exit: errors.New("exit status 1"),
		behave: func(_, logPath string) {
			_ = os.WriteFile(logPath, []byte("FATAL: cannot create user data directory"), 0o600)
		},
	}
	r := newFake(t, f)
	_, err := r.Render(context.Background(), []byte("<html></html>"))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"exit status 1", "cannot create user data directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not include %q: %v", want, err)
		}
	}
}

// A browser that exited after writing the PDF is a success, not a failure.
func TestRenderAcceptsAPDFFromAnExitedBrowser(t *testing.T) {
	f := &fakeLaunch{exits: true, behave: writeFakePDF}
	r := newFake(t, f)
	if _, err := r.Render(context.Background(), []byte("<html></html>")); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

func TestRenderTimesOutWithTheBrowsersWords(t *testing.T) {
	f := &fakeLaunch{behave: func(_, logPath string) {
		_ = os.WriteFile(logPath, []byte("waiting for the renderer"), 0o600)
	}}
	r := newFake(t, f)
	r.Timeout = 80 * time.Millisecond
	_, err := r.Render(context.Background(), []byte("<html></html>"))
	if err == nil {
		t.Fatal("want a timeout")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "waiting for the renderer") {
		t.Fatalf("unhelpful timeout: %v", err)
	}
	if f.stopped == 0 {
		t.Error("a timed-out browser was left running")
	}
}

func TestRenderHonoursCallerCancellation(t *testing.T) {
	f := &fakeLaunch{}
	r := newFake(t, f)
	r.Timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(ctx, []byte("<html></html>")); err == nil {
		t.Fatal("a cancelled request must not keep printing")
	}
}

func TestRenderRejectsEmptyHTML(t *testing.T) {
	r := newFake(t, &fakeLaunch{behave: writeFakePDF})
	if _, err := r.Render(context.Background(), nil); err == nil {
		t.Fatal("empty input must be rejected")
	}
}

func TestRenderLeavesNoTempFiles(t *testing.T) {
	f := &fakeLaunch{behave: writeFakePDF}
	r := newFake(t, f)
	if _, err := r.Render(context.Background(), []byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	src := argValue(f.args, "file://")
	if src == "" {
		t.Fatal("no source path captured")
	}
	if _, err := os.Stat(filepath.Dir(src)); !os.IsNotExist(err) {
		t.Fatalf("temp dir %s survived the render", filepath.Dir(src))
	}
}

func TestBrowserNotFoundIsHonest(t *testing.T) {
	r := New()
	r.getenv = func(string) string { return "" }
	r.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	r.candidates = nil

	if r.Available() {
		t.Fatal("Available must be false with no browser")
	}
	_, err := r.Browser()
	if !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("want ErrNoBrowser, got %v", err)
	}
	for _, want := range []string{EnvBrowser, "google-chrome", "chromium"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
	out, err := r.Render(context.Background(), []byte("<html></html>"))
	if !errors.Is(err, ErrNoBrowser) || out != nil {
		t.Fatalf("Render without a browser: out=%d err=%v", len(out), err)
	}
}

func TestEnvOverrideIsExactAndNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "my-chrome")
	onPath := filepath.Join(dir, "other-chrome")
	for _, p := range []string{bin, onPath} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	r := New()
	r.lookPath = func(string) (string, error) { return onPath, nil }
	r.getenv = func(k string) string {
		if k == EnvBrowser {
			return bin
		}
		return ""
	}
	if got, err := r.Browser(); err != nil || got != bin {
		t.Fatalf("env override ignored: got %q err %v", got, err)
	}

	r.getenv = func(k string) string {
		if k == EnvBrowser {
			return filepath.Join(dir, "does-not-exist")
		}
		return ""
	}
	got, err := r.Browser()
	if err == nil {
		t.Fatalf("a bad override must fail loudly, got %q", got)
	}
	if !strings.Contains(err.Error(), EnvBrowser) || !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error does not name the override: %v", err)
	}
	if strings.Contains(err.Error(), onPath) {
		t.Fatalf("a bad override silently fell back to PATH: %v", err)
	}
}

func TestBrowserPrefersPathBeforeCandidates(t *testing.T) {
	dir := t.TempDir()
	onPath := filepath.Join(dir, "path-chrome")
	candidate := filepath.Join(dir, "candidate-chrome")
	for _, p := range []string{onPath, candidate} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r := New()
	r.getenv = func(string) string { return "" }
	r.candidates = []string{candidate}
	r.lookPath = func(name string) (string, error) {
		if name == "google-chrome" {
			return onPath, nil
		}
		return "", errors.New("not found")
	}
	if got, err := r.Browser(); err != nil || got != onPath {
		t.Fatalf("PATH should win: got %q err %v", got, err)
	}

	r.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if got, err := r.Browser(); err != nil || got != candidate {
		t.Fatalf("candidate fallback: got %q err %v", got, err)
	}
}

func TestNoSandboxIsOptIn(t *testing.T) {
	f := &fakeLaunch{behave: writeFakePDF}
	r := newFake(t, f)
	r.getenv = func(k string) string {
		if k == EnvNoSandbox {
			return "1"
		}
		return ""
	}
	if _, err := r.Render(context.Background(), []byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	if !hasArg(f.args, "--no-sandbox") {
		t.Fatalf("opt-in not honoured: %v", f.args)
	}
}

func TestDefaultCandidatesCoverThisPlatform(t *testing.T) {
	r := New()
	if len(r.candidates) == 0 {
		t.Fatalf("no browser candidates for %s", runtime.GOOS)
	}
	for _, c := range r.candidates {
		if !filepath.IsAbs(c) {
			t.Errorf("candidate is not absolute: %q", c)
		}
	}
}
