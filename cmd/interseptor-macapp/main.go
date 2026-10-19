// Command interseptor-macapp is the Interseptor.app bundle entry point.
//
// It is a thin supervisor, not a second implementation: it locates the real
// interseptor binary shipped alongside it in Contents/MacOS, starts it with the
// browser auto-open suppressed, waits for the control UI to accept connections,
// then opens that URL in an app window (the default browser as a fallback).
// Quitting is forwarded to the child as SIGTERM so the server takes its normal graceful shutdown path.
//
// With --shell it serves the native Interseptor.app shell instead: no window,
// readiness reported on stdout, and stdin EOF treated as "the app quit" (see
// shell.go for the protocol).
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

	"github.com/Veyal/interseptor/internal/appwindow"
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
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "interseptor-macapp:", err)
		os.Exit(2)
	}
	if opts.shell {
		pw := newProtocolWriter(os.Stdout)
		if err := runLauncher(pw.ready, watchEOF(os.Stdin)); err != nil {
			pw.fail(err.Error())
			fmt.Fprintln(os.Stderr, "interseptor-macapp:", err)
			os.Exit(1)
		}
		return
	}
	if err := runLauncher(openURL, nil); err != nil {
		alert(err.Error())
		fmt.Fprintln(os.Stderr, "interseptor-macapp:", err)
		os.Exit(1)
	}
}

// runLauncher wires the real process environment into a launcher. onReady
// receives the control URL once it accepts connections; parentGone is nil in
// window mode and the stdin-EOF lifeline in shell mode.
func runLauncher(onReady func(string), parentGone <-chan struct{}) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate launcher: %w", err)
	}
	bin, err := resolveBinary(filepath.Dir(exe), exec.LookPath)
	if err != nil {
		return err
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

	l := &launcher{
		bin:          bin,
		url:          controlURL(os.Getenv("INTERSEPTOR_CONTROL_ADDR")),
		client:       &http.Client{Timeout: probeTimeout},
		log:          logFile,
		logName:      logFile.Name(),
		signals:      sigCh,
		parentGone:   parentGone,
		onReady:      onReady,
		readyTimeout: readyTimeout,
	}
	return l.run()
}

// launcher starts and supervises one interseptor server.
type launcher struct {
	bin          string
	url          string // best guess at the control URL before the child reports it
	client       *http.Client
	log          io.Writer
	logName      string
	signals      <-chan os.Signal
	parentGone   <-chan struct{} // nil: no parent to watch (window mode)
	onReady      func(url string)
	readyTimeout time.Duration
}

func (l *launcher) run() error {
	// Launching the app while it is already running just brings the UI back up
	// rather than failing on a port conflict. That server belongs to someone
	// else, so shell mode only reports it and never stops it.
	if isInterseptorUp(l.url, l.client) {
		l.onReady(l.url)
		if l.parentGone == nil {
			return nil
		}
		select {
		case <-l.parentGone:
		case <-l.signals:
		}
		return nil
	}

	// The control address resolves CLI → env → persisted setting → default, so the
	// env/default guess above can be wrong. Watch the child's output for the URL
	// it actually bound and switch to that as soon as it appears.
	sniffer := newURLSniffer()
	out := io.MultiWriter(l.log, sniffer)

	cmd := exec.Command(l.bin)
	cmd.Env = append(os.Environ(), "INTERSEPTOR_NO_BROWSER=1")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", l.bin, err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	target := &atomic.Pointer[string]{}
	initial := l.url
	target.Store(&initial)
	go func() {
		if u, ok := <-sniffer.found; ok {
			target.Store(&u)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), l.readyTimeout)
	defer cancel()
	ready := make(chan error, 1)
	go func() {
		ready <- waitReady(ctx, func() bool { return isInterseptorUp(*target.Load(), l.client) }, readyInterval)
	}()

	select {
	case werr := <-exited:
		return fmt.Errorf("interseptor exited during startup (%v) — see %s", werr, l.logName)
	case <-l.signals:
		return stop(cmd, exited)
	case <-l.parentGone:
		return stop(cmd, exited)
	case err := <-ready:
		if err != nil {
			// The child is still running at this point; leaving it would strand a
			// live proxy with no supervisor and no way to quit it from the app.
			_ = stop(cmd, exited)
			return fmt.Errorf("%w — see %s", err, l.logName)
		}
	}
	l.onReady(*target.Load())

	select {
	case err := <-exited:
		if err == nil {
			return nil
		}
		return fmt.Errorf("interseptor exited unexpectedly (%v) — see %s", err, l.logName)
	case <-l.signals:
		return stop(cmd, exited)
	case <-l.parentGone:
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

// openURL shows url in an app window, falling back to the default browser: a
// Finder-launched app has no visible log to point the operator at the URL.
func openURL(url string) {
	if err := appwindow.New().Open(url, appwindow.ProfileDir()); err == nil {
		return
	} else {
		fmt.Fprintln(os.Stderr, "interseptor-macapp: app window unavailable, using the default browser:", err)
	}
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
