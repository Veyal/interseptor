package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := New(func() string { return "9966" })
	t.Cleanup(m.Close)
	return m
}

func TestTunnelHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TUNNEL_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, os.Getenv("TUNNEL_HELPER_URL"))
	select {}
}

func TestQuickTunnelURLRegex(t *testing.T) {
	line := `2024-06-01T00:00:00Z INF |  https://calm-forest-1234.trycloudflare.com   |`
	if got := quickTunnelURL.FindString(line); got != "https://calm-forest-1234.trycloudflare.com" {
		t.Fatalf("URL parse = %q", got)
	}
	if quickTunnelURL.FindString("no url here") != "" {
		t.Fatal("expected no match on a plain line")
	}
}

func TestScanForURLSetsURLAndNotifies(t *testing.T) {
	m := newTestManager(t)
	got := make(chan string, 1)
	m.SetOnURL(func(u string) { got <- u })
	cmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd = cmd
	m.generation = 1
	m.running = true
	m.mu.Unlock()

	stderr := strings.Join([]string{
		"2024 INF Thank you for trying Cloudflare Tunnel.",
		"2024 INF +----------------------------------+",
		"2024 INF |  https://happy-sky-9.trycloudflare.com  |",
		"2024 INF +----------------------------------+",
	}, "\n")
	m.scanForURL(cmd, 1, io.NopCloser(strings.NewReader(stderr)))

	select {
	case u := <-got:
		if u != "https://happy-sky-9.trycloudflare.com" {
			t.Fatalf("callback URL = %q", u)
		}
	case <-time.After(time.Second):
		t.Fatal("onURL was not called")
	}
	if st := m.Status(); st.URL != "https://happy-sky-9.trycloudflare.com" {
		t.Fatalf("Status.URL = %q", st.URL)
	}
}

func TestScanForURLFromStoppedProcessCannotPublishAfterRestart(t *testing.T) {
	m := newTestManager(t)
	got := make(chan string, 2)
	m.SetOnURL(func(u string) { got <- u })

	stoppedCmd := new(exec.Cmd)
	restartedCmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd = stoppedCmd
	m.generation = 1
	m.running = true
	m.mu.Unlock()
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case u := <-got:
		if u != "" {
			t.Fatalf("Stop callback = %q, want empty URL", u)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not publish an empty URL")
	}

	// Model the successful portion of restart without launching an external
	// cloudflared process, then deliver buffered output from the stopped one.
	m.mu.Lock()
	m.cmd = restartedCmd
	m.generation++
	restartedGeneration := m.generation
	m.running = true
	m.url = ""
	m.mu.Unlock()
	m.scanForURL(stoppedCmd, 1, strings.NewReader("https://stale.trycloudflare.com\n"))

	m.scanForURL(restartedCmd, restartedGeneration, strings.NewReader("https://fresh.trycloudflare.com\n"))

	select {
	case url := <-got:
		if url != "https://fresh.trycloudflare.com" {
			t.Fatalf("published URL = %q, want restarted tunnel URL", url)
		}
	case <-time.After(time.Second):
		t.Fatal("restarted tunnel URL was not published")
	}
	select {
	case url := <-got:
		t.Fatalf("unexpected extra URL publication: %q", url)
	default:
	}
	if st := m.Status(); st.URL != "https://fresh.trycloudflare.com" {
		t.Fatalf("Status.URL = %q, want restarted tunnel URL", st.URL)
	}
}

func TestStaleURLPublicationCanceledAcrossStopRestart(t *testing.T) {
	m := newTestManager(t)
	var eventsMu sync.Mutex
	var events []string
	oldEntered := make(chan struct{})
	releaseOld := make(chan struct{})
	allDelivered := make(chan struct{})
	m.beforeDeliver = func(u string) {
		if u == "https://old.trycloudflare.com" {
			close(oldEntered)
			<-releaseOld
		}
	}
	m.SetOnURL(func(u string) {
		eventsMu.Lock()
		events = append(events, u)
		count := len(events)
		eventsMu.Unlock()
		if count == 2 {
			close(allDelivered)
		}
	})

	oldCmd := new(exec.Cmd)
	newCmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd = oldCmd
	m.generation = 1
	m.running = true
	m.mu.Unlock()

	scanDone := make(chan struct{})
	go func() {
		m.scanForURL(oldCmd, 1, strings.NewReader("https://old.trycloudflare.com\n"))
		close(scanDone)
	}()
	<-oldEntered

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	m.mu.Lock()
	m.cmd = newCmd
	m.generation++
	m.running = true
	m.mu.Unlock()
	close(releaseOld)
	<-scanDone
	m.mu.Lock()
	newGeneration := m.generation
	m.mu.Unlock()
	m.scanForURL(newCmd, newGeneration, strings.NewReader("https://new.trycloudflare.com\n"))
	select {
	case <-allDelivered:
	case <-time.After(time.Second):
		t.Fatal("stale-safe callback delivery did not complete")
	}

	eventsMu.Lock()
	got := append([]string(nil), events...)
	eventsMu.Unlock()
	want := []string{"", "https://new.trycloudflare.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publication order = %v, want %v", got, want)
	}
}

func TestAsyncDispatcherOrdersExecutingOldCallbackBeforeClearAndNewURL(t *testing.T) {
	m := newTestManager(t)
	t.Cleanup(func() { _ = m.Stop() })
	m.lookPath = func(string) (string, error) { return os.Args[0], nil }
	startCount := 0
	m.commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		startCount++
		url := "https://old.trycloudflare.com"
		if startCount == 2 {
			url = "https://new.trycloudflare.com"
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestTunnelHelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_TUNNEL_HELPER=1",
			"TUNNEL_HELPER_URL="+url,
		)
		return cmd
	}
	var eventsMu sync.Mutex
	var events []string
	oldEntered := make(chan struct{})
	releaseOld := make(chan struct{})
	allDelivered := make(chan struct{})
	m.SetOnURL(func(u string) {
		eventsMu.Lock()
		events = append(events, u)
		count := len(events)
		eventsMu.Unlock()
		if u == "https://old.trycloudflare.com" {
			close(oldEntered)
			<-releaseOld
		}
		if count == 3 {
			close(allDelivered)
		}
	})

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("start old tunnel: %v", err)
	}
	<-oldEntered

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("start new tunnel while old callback is paused: %v", err)
	}
	close(releaseOld)

	select {
	case <-allDelivered:
	case <-time.After(time.Second):
		t.Fatal("ordered callback delivery did not complete")
	}
	eventsMu.Lock()
	got := append([]string(nil), events...)
	eventsMu.Unlock()
	want := []string{"https://old.trycloudflare.com", "", "https://new.trycloudflare.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivery order = %v, want %v", got, want)
	}
}

func TestStopPublishesEmptyExactlyOnceDespiteStaleWaitExit(t *testing.T) {
	m := newTestManager(t)
	got := make(chan string, 2)
	m.SetOnURL(func(u string) {
		_ = m.Status() // callback must run outside the manager state mutex
		got <- u
	})
	cmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd = cmd
	m.generation = 1
	m.running = true
	m.url = "https://live.trycloudflare.com"
	m.mu.Unlock()

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case u := <-got:
		if u != "" {
			t.Fatalf("Stop callback = %q, want empty URL", u)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not publish an empty URL")
	}

	m.processWG.Add(1)
	m.waitExit(cmd, 1)
	select {
	case u := <-got:
		t.Fatalf("stale waitExit published duplicate callback %q", u)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestURLCallbackCanStopTunnelReentrantly(t *testing.T) {
	m := newTestManager(t)
	cmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd = cmd
	m.generation = 1
	m.running = true
	m.mu.Unlock()

	done := make(chan struct{})
	var eventsMu sync.Mutex
	var events []string
	m.SetOnURL(func(u string) {
		eventsMu.Lock()
		events = append(events, u)
		eventsMu.Unlock()
		if u != "" {
			if err := m.Stop(); err != nil {
				t.Errorf("reentrant Stop: %v", err)
			}
		} else {
			close(done)
		}
	})

	go m.scanForURL(cmd, 1, strings.NewReader("https://live.trycloudflare.com\n"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("URL callback deadlocked calling Stop")
	}

	eventsMu.Lock()
	got := append([]string(nil), events...)
	eventsMu.Unlock()
	want := []string{"https://live.trycloudflare.com", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("callback events = %v, want %v", got, want)
	}
}

func TestStopCallbackCanCallStartReentrantly(t *testing.T) {
	m := newTestManager(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	m.mu.Lock()
	m.cmd = new(exec.Cmd)
	m.generation = 1
	m.running = true
	m.mu.Unlock()

	startResult := make(chan error, 1)
	m.SetOnURL(func(u string) {
		if u == "" {
			_, err := m.Start(context.Background())
			startResult <- err
		}
	})

	stopDone := make(chan error, 1)
	go func() { stopDone <- m.Stop() }()
	select {
	case err := <-startResult:
		if err == nil {
			t.Fatal("reentrant Start unexpectedly found cloudflared")
		}
	case <-time.After(time.Second):
		t.Fatal("stop callback deadlocked calling Start")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after reentrant Start")
	}
}

func TestInstalledUsesLookPath(t *testing.T) {
	m := newTestManager(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if m.Installed() {
		t.Fatal("Installed should be false when lookPath fails")
	}
	m.lookPath = func(string) (string, error) { return "/usr/bin/cloudflared", nil }
	if !m.Installed() {
		t.Fatal("Installed should be true when lookPath succeeds")
	}
}

func TestStartFailsWithoutBinary(t *testing.T) {
	m := newTestManager(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	st, err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected an error when cloudflared is absent")
	}
	if st.Running {
		t.Fatal("status must not be running when start failed")
	}
	if st.Err == "" {
		t.Fatal("expected a lastErr message")
	}
}

func TestCloseReapsChildAndRejectsFutureStart(t *testing.T) {
	m := New(func() string { return "9966" })
	m.lookPath = func(string) (string, error) { return os.Args[0], nil }
	m.commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestTunnelHelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_TUNNEL_HELPER=1",
			"TUNNEL_HELPER_URL=https://close-test.trycloudflare.com",
		)
		return cmd
	}

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	m.Close()
	m.Close() // idempotent

	if cmd.ProcessState == nil {
		t.Fatal("Close returned before cloudflared child was reaped")
	}
	if _, err := m.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Close error = %v, want ErrClosed", err)
	}
}
