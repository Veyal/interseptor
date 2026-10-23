package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		shell   bool
		wantErr bool
	}{
		{"no args is window mode", nil, false, false},
		{"--shell", []string{"--shell"}, true, false},
		{"-shell", []string{"-shell"}, true, false},
		// Finder used to pass a process serial number to launched apps.
		{"psn argument is ignored", []string{"-psn_0_12345"}, false, false},
		{"unknown flag", []string{"--bogus"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOptions(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseOptions(%q) err = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if err == nil && got.shell != tt.shell {
				t.Errorf("parseOptions(%q).shell = %v, want %v", tt.args, got.shell, tt.shell)
			}
		})
	}
}

func TestProtocolLines(t *testing.T) {
	if got := readyLine("http://127.0.0.1:9966"); got != "INTERSEPTOR_READY http://127.0.0.1:9966\n" {
		t.Errorf("readyLine = %q", got)
	}
	// The native shell reads one message per line, so a multi-line error must
	// not be able to spill into lines it would misparse.
	got := errorLine("start failed\nsecond line\r\nthird")
	if got != "INTERSEPTOR_ERROR start failed second line third\n" {
		t.Errorf("errorLine = %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("errorLine must be exactly one line, got %q", got)
	}
}

func TestWatchEOFClosesWhenReaderEnds(t *testing.T) {
	r, w := io.Pipe()
	done := watchEOF(r)
	select {
	case <-done:
		t.Fatal("closed before EOF")
	case <-time.After(20 * time.Millisecond):
	}
	w.Write([]byte("noise is ignored\n"))
	w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not close after EOF")
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer for capturing protocol output.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func fakeControlPlane(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		interseptorJSON(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// An already-running server belongs to someone else: shell mode reports it and
// waits for the parent to go away, but must never start (or stop) anything.
func TestShellModeAttachesToRunningServerWithoutOwningIt(t *testing.T) {
	srv := fakeControlPlane(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	bin := filepath.Join(dir, serverBinaryName)
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &syncBuffer{}
	parentGone := make(chan struct{})
	l := &launcher{
		bin:          bin,
		url:          srv.URL,
		client:       srv.Client(),
		log:          io.Discard,
		logName:      "test.log",
		signals:      make(chan os.Signal),
		parentGone:   parentGone,
		onReady:      func(u string) { io.WriteString(out, readyLine(u)) },
		readyTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- l.run() }()

	waitFor(t, func() bool { return strings.Contains(out.String(), "INTERSEPTOR_READY "+srv.URL) })
	select {
	case err := <-errCh:
		t.Fatalf("returned before the parent went away: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(parentGone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not return after the parent went away")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a server was started even though one was already running")
	}
	if n := strings.Count(out.String(), "INTERSEPTOR_READY"); n != 1 {
		t.Errorf("READY reported %d times, want exactly once", n)
	}
}

// A started child is supervised: READY is reported with the URL the child
// logged, and the parent going away stops the child gracefully.
func TestShellModeStartsChildAndStopsItWhenParentGoes(t *testing.T) {
	srv := fakeControlPlane(t)
	dir := t.TempDir()
	stopped := filepath.Join(dir, "stopped")
	bin := filepath.Join(dir, serverBinaryName)
	script := "#!/bin/sh\n" +
		"trap 'touch " + stopped + "; exit 0' TERM\n" +
		"echo \"Interseptor vX · UI on " + srv.URL + " · data /x\"\n" +
		"while :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &syncBuffer{}
	parentGone := make(chan struct{})
	l := &launcher{
		bin:          bin,
		url:          "http://127.0.0.1:1", // wrong guess; the sniffed URL must win
		client:       srv.Client(),
		log:          io.Discard,
		logName:      "test.log",
		signals:      make(chan os.Signal),
		parentGone:   parentGone,
		onReady:      func(u string) { io.WriteString(out, readyLine(u)) },
		readyTimeout: 5 * time.Second,
	}
	// The pre-flight probe must not see the fake server under the wrong guess.
	errCh := make(chan error, 1)
	go func() { errCh <- l.run() }()

	waitFor(t, func() bool { return strings.Contains(out.String(), "INTERSEPTOR_READY "+srv.URL) })
	close(parentGone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after the parent went away")
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Error("child was not sent SIGTERM when the parent went away")
	}
}

// The child gets a lifeline: a stdin pipe the launcher holds open plus the env
// var telling the server to shut down at EOF, so a SIGKILLed launcher can never
// orphan it. With the old /dev/null stdin the read below would hit EOF at once.
func TestShellModeGivesChildAStdinLifeline(t *testing.T) {
	srv := fakeControlPlane(t)
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	eof := filepath.Join(dir, "eof")
	bin := filepath.Join(dir, serverBinaryName)
	script := "#!/bin/sh\n" +
		"echo \"$INTERSEPTOR_EXIT_ON_STDIN_EOF\" > " + envFile + "\n" +
		"echo \"Interseptor vX · UI on " + srv.URL + " · data /x\"\n" +
		"while read -r _; do :; done\n" +
		"touch " + eof + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	parentGone := make(chan struct{})
	l := &launcher{
		bin:          bin,
		url:          "http://127.0.0.1:1",
		client:       srv.Client(),
		log:          io.Discard,
		logName:      "test.log",
		signals:      make(chan os.Signal),
		parentGone:   parentGone,
		onReady:      func(u string) { io.WriteString(out, readyLine(u)) },
		readyTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- l.run() }()

	waitFor(t, func() bool { return strings.Contains(out.String(), "INTERSEPTOR_READY ") })
	if b, _ := os.ReadFile(envFile); strings.TrimSpace(string(b)) != "1" {
		t.Errorf("child env INTERSEPTOR_EXIT_ON_STDIN_EOF = %q, want 1", strings.TrimSpace(string(b)))
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(eof); err == nil {
		t.Error("child saw stdin EOF while the launcher was still running")
	}
	close(parentGone)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not return after the parent went away")
	}
}

func TestShellModeReportsChildExitDuringStartup(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, serverBinaryName)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := &launcher{
		bin:          bin,
		url:          "http://127.0.0.1:1",
		client:       &http.Client{Timeout: 100 * time.Millisecond},
		log:          io.Discard,
		logName:      "test.log",
		signals:      make(chan os.Signal),
		parentGone:   make(chan struct{}),
		onReady:      func(string) { t.Error("READY reported for a child that died") },
		readyTimeout: 5 * time.Second,
	}
	err := l.run()
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("run err = %v, want an exited-during-startup error", err)
	}
}

// writeProtocol must emit only protocol lines on stdout, flushing each one so
// the native shell sees READY immediately rather than at exit.
func TestProtocolWriterFlushesEachLine(t *testing.T) {
	r, w := io.Pipe()
	pw := newProtocolWriter(w)
	go pw.ready("http://127.0.0.1:19996")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "INTERSEPTOR_READY http://127.0.0.1:19996\n" {
		t.Errorf("got %q", line)
	}
}

// A protocol write after the shell closed its end of stdout must fail with
// EPIPE, not kill the supervisor with SIGPIPE (which would orphan its server).
// The startup error path writes INTERSEPTOR_ERROR, so no server is needed.
func TestShellModeSurvivesClosedStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the launcher binary")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "interseptor-macapp")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer w.Close()
	cmd := exec.Command(exe, "--shell")
	// No interseptor next to the launcher or on PATH: startup fails at once.
	cmd.Env = []string{"PATH=" + dir, "HOME=" + dir}
	cmd.Stdout = w
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("run err = %v, want a non-zero exit", err)
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("killed by %v, want a normal exit with EPIPE ignored", ws.Signal())
	}
	if ee.ExitCode() != 1 {
		t.Errorf("exit code %d, want 1", ee.ExitCode())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// TestSwiftSupervisorStateMachine compiles the native shell's Supervisor with
// plain swiftc and runs its state-machine tests (stop timeout escalation,
// restart after a forced stop, stale exits). macOS with swiftc only.
func TestSwiftSupervisorStateMachine(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS swiftc")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not available")
	}
	out, err := exec.Command("bash", filepath.Join("..", "..", "packaging", "macos", "shell-tests", "run.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("shell tests failed: %v\n%s", err, out)
	}
}

// TestMenuPanelsFollowRailOrder keeps the native View menu in the web rail's
// order, with unique shortcuts.
func TestMenuPanelsFollowRailOrder(t *testing.T) {
	menu, err := os.ReadFile(filepath.Join("..", "..", "packaging", "macos", "shell", "MainMenu.swift"))
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join("..", "..", "internal", "control", "ui", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	var menuIDs []string
	seen := map[string]string{}
	for _, m := range regexp.MustCompile(`PanelItem\(title: "[^"]+", id: "([a-z]+)", key: "([0-9]*)"(, mods: \[[^\]]*\])?\)`).FindAllStringSubmatch(string(menu), -1) {
		menuIDs = append(menuIDs, m[1])
		if m[2] == "" {
			continue
		}
		sc := m[2] + m[3]
		if prev, dup := seen[sc]; dup {
			t.Errorf("shortcut %q used by %s and %s", sc, prev, m[1])
		}
		seen[sc] = m[1]
	}
	var railIDs []string
	for _, m := range regexp.MustCompile(`class="tab[^"]*" id="tab-[a-z]+"[^>]*data-tab="([a-z]+)"`).FindAllStringSubmatch(string(html), -1) {
		railIDs = append(railIDs, m[1])
	}
	if strings.Join(menuIDs, ",") != strings.Join(railIDs, ",") {
		t.Errorf("menu panels %v != rail %v", menuIDs, railIDs)
	}
}
