// Command interseptor-macapp is the Interseptor.app bundle entry point.
//
// It is a thin supervisor, not a second implementation: it locates the real
// interseptor binary shipped alongside it in Contents/MacOS, starts it with the
// browser auto-open suppressed, waits for the control UI to accept connections,
// then opens that URL in the default browser. Quitting is forwarded to the
// child as SIGTERM so the server takes its normal graceful shutdown path.
//
// A double-clicked app has nowhere to print to, so startup failures surface as
// a native alert and the child's output is tee'd to a log file under
// ~/Library/Logs.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	readyTimeout  = 60 * time.Second
	readyInterval = 250 * time.Millisecond
	probeTimeout  = 1500 * time.Millisecond
	// shutdownGrace matches the SIGTERM-then-SIGKILL window `interseptor stop`
	// uses, so quitting the app behaves the same as stopping it from the CLI.
	shutdownGrace = 6 * time.Second
)

func main() {
	if err := run(); err != nil {
		alert(err.Error())
		fmt.Fprintln(os.Stderr, "interseptor-macapp:", err)
		os.Exit(1)
	}
}

func run() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate launcher: %w", err)
	}
	bin, err := resolveBinary(filepath.Dir(exe), exec.LookPath)
	if err != nil {
		return err
	}

	url := controlURL(os.Getenv("INTERSEPTOR_CONTROL_ADDR"))
	client := &http.Client{Timeout: probeTimeout}

	// Launching the app while it is already running just brings the UI back up
	// rather than failing on a port conflict.
	if isInterseptorUp(url, client) {
		openURL(url)
		return nil
	}

	logFile, err := openLog()
	if err != nil {
		return err
	}
	defer logFile.Close()

	// Arm signal handling before the child exists. Registering it later leaves a
	// window where a quit during startup kills this process outright and orphans
	// a running proxy with no supervisor.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// The control address resolves CLI → env → persisted setting → default, so the
	// env/default guess above can be wrong. Watch the child's output for the URL
	// it actually bound and switch to that as soon as it appears.
	sniffer := newURLSniffer()
	out := io.MultiWriter(logFile, sniffer)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "INTERSEPTOR_NO_BROWSER=1")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	target := &atomic.Pointer[string]{}
	target.Store(&url)
	go func() {
		if u, ok := <-sniffer.found; ok {
			target.Store(&u)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	ready := make(chan error, 1)
	go func() {
		ready <- waitReady(ctx, func() bool { return isInterseptorUp(*target.Load(), client) }, readyInterval)
	}()

	select {
	case werr := <-exited:
		return fmt.Errorf("interseptor exited during startup (%v) — see %s", werr, logFile.Name())
	case <-sigCh:
		return stop(cmd, exited)
	case err := <-ready:
		if err != nil {
			// The child is still running at this point; leaving it would strand a
			// live proxy with no supervisor and no way to quit it from the app.
			_ = stop(cmd, exited)
			return fmt.Errorf("%w — see %s", err, logFile.Name())
		}
	}
	openURL(*target.Load())

	select {
	case err := <-exited:
		return err
	case <-sigCh:
		return stop(cmd, exited)
	}
}

// stop asks the server to shut down gracefully, escalating to SIGKILL if it
// overstays the grace window.
func stop(cmd *exec.Cmd, exited <-chan error) error {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
		return nil
	case <-time.After(shutdownGrace):
		_ = cmd.Process.Kill()
		return nil
	}
}

// openLog appends to ~/Library/Logs/Interseptor.log, falling back to the
// temp dir when the home directory is unavailable.
func openLog() (*os.File, error) {
	dir, err := os.UserHomeDir()
	if err == nil {
		dir = filepath.Join(dir, "Library", "Logs")
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			dir = os.TempDir()
		}
	} else {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "Interseptor.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return f, nil
}

// openURL hands url to the default browser.
func openURL(url string) {
	if path, err := exec.LookPath("open"); err == nil {
		_ = exec.Command(path, url).Start()
	}
}

// alert shows a native dialog, the only way a bundled app can report a startup
// failure to someone who launched it from Finder.
func alert(msg string) {
	path, err := exec.LookPath("osascript")
	if err != nil {
		return
	}
	script := fmt.Sprintf("display alert %q message %q as critical", "Interseptor could not start", msg)
	_ = exec.Command(path, "-e", script).Run()
}
