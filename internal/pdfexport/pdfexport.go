// Package pdfexport prints a self-contained HTML report to PDF with a headless
// Chrome. It is the one place in Interseptor that depends on an external
// browser, deliberately kept out of internal/report so report rendering stays
// pure, deterministic and testable without a browser installed.
//
// The browser is optional. Nothing else in Interseptor needs it, and when it is
// absent every function here fails with ErrNoBrowser and an error that names the
// override variable and the binaries that were looked for — never a silent
// fallback to a different format and never a partially written file.
//
// # Why this waits for the file and not for the process
//
// The obvious implementation — run `chrome --headless --print-to-pdf` and read
// the file once the process exits — does not work. Chrome 154 on macOS writes
// the PDF in about a second and then keeps running indefinitely: background
// networking, crash reporting and the auto-updater outlive the print. Waiting
// for the process means waiting forever, and reading its output through a pipe
// is worse, because the updater it spawns inherits that pipe and holds it open
// after Chrome itself is gone.
//
// So Render waits for the artifact: it starts the browser with its output
// redirected to a log file (never a pipe), polls for a PDF that is complete —
// %PDF- header, %%EOF trailer, a size that has stopped changing — reads it, and
// then terminates the browser and everything it started. A browser that exits
// early, or never produces a complete PDF, is reported as the failure it is,
// with the tail of what the browser said.
package pdfexport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/chromemgr"
)

// EnvBrowser names an exact Chrome/Chromium binary to use. When it is set and
// the path is not an executable file, discovery fails rather than falling back:
// an operator who pointed at a browser deserves to be told it was not usable.
const EnvBrowser = "INTERSEPTOR_CHROME"

// EnvNoSandbox opts into Chrome's --no-sandbox. It is off by default because
// the sandbox is a real boundary around a process that renders captured
// evidence; set it only where the sandbox cannot start (a container running as
// root, typically).
const EnvNoSandbox = "INTERSEPTOR_CHROME_NO_SANDBOX"

// ErrNoBrowser reports that no headless Chrome could be located.
var ErrNoBrowser = errors.New("no headless Chrome found")

// browserNames are looked up on PATH, in order.
var browserNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"chrome", "microsoft-edge", "msedge",
}

// DefaultTimeout bounds one print. A report is a single local page with no
// network access, so a print that has not produced a PDF by now is stuck.
const DefaultTimeout = 60 * time.Second

const (
	// pollInterval is how often the output file is checked for completeness.
	pollInterval = 50 * time.Millisecond
	// maxBrowserOutput bounds how much of the browser's own log is quoted in an
	// error.
	maxBrowserOutput = 2 << 10
)

// Renderer prints HTML to PDF. The environment, filesystem and process seams are
// fields so the package is testable without a browser.
type Renderer struct {
	// Timeout bounds one print; zero means DefaultTimeout.
	Timeout time.Duration

	getenv     func(string) string
	lookPath   func(string) (string, error)
	candidates []string
	launch     launcher
	poll       time.Duration
}

// launcher starts the browser with its stdout and stderr appended to logPath.
// exited is closed (carrying the process error, if any) when the browser's own
// process ends; stop terminates the browser and every process it started and
// must be safe to call more than once.
type launcher func(ctx context.Context, bin string, args []string, logPath string) (exited <-chan error, stop func(), err error)

// New returns a Renderer that discovers a browser from the environment, PATH
// and the usual install locations for this platform.
func New() *Renderer {
	return &Renderer{
		getenv:     os.Getenv,
		lookPath:   exec.LookPath,
		candidates: browserCandidates(),
		launch:     launchBrowser,
		poll:       pollInterval,
	}
}

// Browser resolves the browser that will be used, or an error naming what is
// missing. It never returns a path it could not stat as an executable file.
func (r *Renderer) Browser() (string, error) {
	if override := strings.TrimSpace(r.getenv(EnvBrowser)); override != "" {
		if err := executableFile(override); err != nil {
			return "", fmt.Errorf("%s=%s is not usable: %w", EnvBrowser, override, err)
		}
		return override, nil
	}
	for _, name := range browserNames {
		if path, err := r.lookPath(name); err == nil && executableFile(path) == nil {
			return path, nil
		}
	}
	for _, path := range r.candidates {
		if executableFile(path) == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: set %s to a Chrome or Chromium binary, or install one of %s",
		ErrNoBrowser, EnvBrowser, strings.Join(browserNames, ", "))
}

// Available reports whether a browser could be resolved.
func (r *Renderer) Available() bool {
	_, err := r.Browser()
	return err == nil
}

func executableFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

// Render prints html to PDF bytes. html must be a self-contained document: the
// browser gets no access to the control API, so every stylesheet and image has
// to be inline already (report.ProjectHTML embeds both).
//
// The returned error is the whole story on failure — Render never returns
// partial output next to an error, and never substitutes another format.
func (r *Renderer) Render(ctx context.Context, html []byte) ([]byte, error) {
	if len(html) == 0 {
		return nil, errors.New("pdf export: nothing to render")
	}
	bin, err := r.Browser()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "interseptor-report-pdf-")
	if err != nil {
		return nil, fmt.Errorf("pdf export: %w", err)
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "report.html")
	if err := os.WriteFile(src, html, 0o600); err != nil {
		return nil, fmt.Errorf("pdf export: %w", err)
	}
	out := filepath.Join(dir, "report.pdf")
	logPath := filepath.Join(dir, "browser.log")

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	exited, stop, err := r.launch(runCtx, bin, r.args(dir, src, out), logPath)
	if err != nil {
		return nil, fmt.Errorf("pdf export: cannot start %s: %w", filepath.Base(bin), err)
	}
	defer stop()

	name := filepath.Base(bin)
	pdf, err := r.awaitPDF(runCtx, out, exited)
	if err != nil {
		return nil, fmt.Errorf("pdf export: %s %w%s", name, err, browserLog(logPath))
	}
	return pdf, nil
}

// awaitPDF returns the finished PDF, or explains why there is not one. It
// deliberately does not require the browser to exit; see the package comment.
func (r *Renderer) awaitPDF(ctx context.Context, out string, exited <-chan error) ([]byte, error) {
	interval := r.poll
	if interval <= 0 {
		interval = pollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastSize int64 = -1
	// browserGone records that the process ended; one more poll then decides,
	// because the PDF may have been completed just before it exited.
	browserGone := false
	var exitErr error
	for {
		data, size, complete := readCompletePDF(out)
		switch {
		case complete && size == lastSize:
			return data, nil
		case complete:
			// Seen once: confirm on the next tick that it stopped growing.
			lastSize = size
		default:
			lastSize = -1
			if head := invalidPDFHead(out); head != "" {
				return nil, fmt.Errorf("output is not a PDF (starts %q)", head)
			}
		}
		if browserGone {
			if exitErr != nil {
				return nil, fmt.Errorf("failed: %w", exitErr)
			}
			return nil, errors.New("exited without writing a complete PDF")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out after %s without a complete PDF", r.timeout())
		case exitErr = <-exited:
			browserGone = true
		case <-ticker.C:
		}
	}
}

func (r *Renderer) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// readCompletePDF reports whether out holds a finished PDF: the %PDF- header and
// the %%EOF trailer that closes the file.
func readCompletePDF(out string) (data []byte, size int64, complete bool) {
	data, err := os.ReadFile(out)
	if err != nil || len(data) < 8 {
		return nil, 0, false
	}
	if !strings.HasPrefix(string(data), "%PDF-") {
		return nil, int64(len(data)), false
	}
	tail := data
	if len(tail) > 1024 {
		tail = tail[len(tail)-1024:]
	}
	if !strings.Contains(string(tail), "%%EOF") {
		return nil, int64(len(data)), false
	}
	return data, int64(len(data)), true
}

// invalidPDFHead returns a short prefix of out when it exists but is plainly not
// a PDF, so a browser that wrote an error page is reported instead of waited on.
func invalidPDFHead(out string) string {
	f, err := os.Open(out)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 16)
	n, _ := f.Read(buf)
	if n < 5 {
		return ""
	}
	if strings.HasPrefix(string(buf[:n]), "%PDF-") {
		return ""
	}
	return string(buf[:n])
}

// args builds the print command. The profile lives inside the same temp dir, so
// a print never touches the operator's own Chrome profile or open tabs, and the
// background services are switched off: exporting a report should not phone
// home, check for updates or file a crash report.
func (r *Renderer) args(dir, src, out string) []string {
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--hide-scrollbars",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-sync",
		"--disable-breakpad",
		"--disable-crash-reporter",
		"--disable-domain-reliability",
		"--disable-client-side-phishing-detection",
		"--metrics-recording-only",
		"--no-pings",
		"--no-service-autorun",
		// Suppress Chrome's default header/footer: it prints the page title and
		// the source URL, which would put a local temp path in a deliverable.
		"--no-pdf-header-footer",
		"--user-data-dir=" + filepath.Join(dir, "profile"),
	}
	if boolEnv(r.getenv(EnvNoSandbox)) {
		args = append(args, "--no-sandbox")
	}
	return append(args, "--print-to-pdf="+out, fileURL(src))
}

// fileURL builds an escaped file:// URL for a local path, so a temp directory
// containing '#', '%' or spaces still names the exact file ('#' would
// otherwise start a fragment and '%' an escape sequence).
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive path: file:///C:/...
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func boolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// browserLog returns the tail of what the browser wrote, for an error message.
func browserLog(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return ""
	}
	if len(s) > maxBrowserOutput {
		s = s[len(s)-maxBrowserOutput:]
	}
	return ": " + s
}

// launchBrowser starts the browser for real. Output goes to a file rather than a
// pipe so a child process that outlives the browser cannot keep us waiting on
// it; see the package comment.
func launchBrowser(ctx context.Context, bin string, args []string, logPath string) (<-chan error, func(), error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}
	// CommandContext so an abandoned print cannot outlive its deadline even if
	// stop is never reached; stop still kills the whole tree on the normal path.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	isolateProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, nil, err
	}
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()
	var stopped bool
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		terminateProcessGroup(cmd)
		log.Close()
	}
	return exited, stop, nil
}

// browserCandidates are the usual absolute install locations for this platform,
// tried after PATH. Includes managed Chrome from ~/.interseptor/chrome.
func browserCandidates() []string {
	var managed []string
	if dir := chromemgr.Dir(os.Getenv("INTERSEPTOR_DATA_DIR")); dir != "" {
		managed = chromemgr.CandidatePaths(dir, runtime.GOOS, runtime.GOARCH)
	}

	var system []string
	switch runtime.GOOS {
	case "darwin":
		system = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "windows":
		var dirs []string
		for _, v := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := os.Getenv(v); base != "" {
				dirs = append(dirs, base)
			}
		}
		for _, base := range dirs {
			system = append(system,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Chromium\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
			)
		}
	default:
		system = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
			"/usr/bin/microsoft-edge",
		}
	}
	return append(managed, system...)
}
