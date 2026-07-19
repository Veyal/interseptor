// Package tunnel manages a Cloudflare "quick tunnel" (cloudflared) as a child
// process, exposing the local control plane at a public https://*.trycloudflare.com
// URL with no account or public IP required. It parses the assigned URL from
// cloudflared's stderr and surfaces start/stop/status so the UI can drive it.
//
// cloudflared is an EXTERNAL binary (not a Go dependency): the manager detects it
// on PATH and never downloads it — a supply-chain-hygiene choice matching how the
// project treats adb/idevice tooling. When it is absent, Status.Installed is false
// and the UI shows install guidance.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// quickTunnelURL matches the assigned trycloudflare URL in cloudflared's stderr.
var quickTunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// ErrClosed is returned when Start is called after Manager.Close.
var ErrClosed = errors.New("tunnel manager is closed")

// Status is a snapshot of the tunnel manager's state.
type Status struct {
	Installed bool   `json:"installed"` // cloudflared is on PATH
	Running   bool   `json:"running"`   // a tunnel process is live
	URL       string `json:"url"`       // public URL (fills in shortly after start)
	Err       string `json:"err"`       // last error (start failure / crash)
	StartedAt int64  `json:"startedAt"` // unix millis
}

type notification struct {
	generation uint64
	url        string
	cb         func(string)
}

// Manager owns at most one cloudflared process at a time.
type Manager struct {
	controlPort func() string // returns the loopback control port to expose

	mu                sync.Mutex
	cmd               *exec.Cmd
	cancel            context.CancelFunc
	generation        uint64
	running           bool
	url               string
	lastErr           string
	startedAt         int64
	onURL             func(string) // notified once the URL is known (and on stop, with "")
	notifications     []notification
	dispatcherWake    chan struct{}
	dispatcherStop    chan struct{}
	dispatcherDone    chan struct{}
	dispatcherStarted bool
	dispatcherClosed  bool
	closed            bool
	closeStarted      bool
	closeDone         chan struct{}
	processWG         sync.WaitGroup

	beforeDeliver func(string) // test hook: runs before final generation validation

	// lookPath / now are injected so tests can stub the binary and clock.
	lookPath       func(string) (string, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
	nowMs          func() int64
}

// New builds a Manager. controlPort returns the loopback port string (e.g. "9966")
// the tunnel should forward to.
func New(controlPort func() string) *Manager {
	return &Manager{
		controlPort:    controlPort,
		lookPath:       exec.LookPath,
		commandContext: exec.CommandContext,
		nowMs:          func() int64 { return time.Now().UnixMilli() },
		closeDone:      make(chan struct{}),
	}
}

// SetOnURL registers a callback fired when the public URL becomes known, and again
// with "" when the tunnel stops. Used to broadcast a live UI update.
func (m *Manager) SetOnURL(fn func(string)) {
	m.mu.Lock()
	m.onURL = fn
	m.mu.Unlock()
}

// Installed reports whether cloudflared is available on PATH.
func (m *Manager) Installed() bool {
	_, err := m.lookPath("cloudflared")
	return err == nil
}

// Status returns a snapshot.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Installed: m.Installed(),
		Running:   m.running,
		URL:       m.url,
		Err:       m.lastErr,
		StartedAt: m.startedAt,
	}
}

// Start launches cloudflared if not already running. It is idempotent — a second
// call while running just returns the current status. The public URL is filled in
// asynchronously as cloudflared reports it. The caller is responsible for ensuring
// authentication is configured before exposing the surface (see the control layer,
// which refuses to start a tunnel with no API keys).
func (m *Manager) Start(ctx context.Context) (Status, error) {
	m.mu.Lock()
	if m.closed {
		m.lastErr = ErrClosed.Error()
		st := m.statusLocked()
		m.mu.Unlock()
		return st, ErrClosed
	}
	if m.running {
		st := m.statusLocked()
		m.mu.Unlock()
		return st, nil
	}
	bin, err := m.lookPath("cloudflared")
	if err != nil {
		m.lastErr = "cloudflared not found on PATH"
		st := m.statusLocked()
		m.mu.Unlock()
		return st, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	target := "http://127.0.0.1:" + m.controlPort()
	cmd := m.commandContext(runCtx, bin, "tunnel", "--url", target, "--no-autoupdate")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		m.lastErr = err.Error()
		st := m.statusLocked()
		m.mu.Unlock()
		return st, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		m.lastErr = err.Error()
		st := m.statusLocked()
		m.mu.Unlock()
		return st, err
	}
	m.cmd = cmd
	m.cancel = cancel
	m.generation++
	generation := m.generation
	m.running = true
	m.url = ""
	m.lastErr = ""
	m.startedAt = m.nowMs()
	m.processWG.Add(1)
	m.mu.Unlock()

	go m.scanForURL(cmd, generation, stderr)
	go m.waitExit(cmd, generation)

	return m.Status(), nil
}

// scanForURL reads cloudflared's stderr, extracting the first trycloudflare URL.
func (m *Manager) scanForURL(cmd *exec.Cmd, generation uint64, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if u := quickTunnelURL.FindString(sc.Text()); u != "" {
			m.publishURL(cmd, generation, u)
			return
		}
	}
	// Drain the rest so the pipe doesn't block the child on a full buffer.
	_, _ = io.Copy(io.Discard, r)
}

func (m *Manager) publishURL(cmd *exec.Cmd, generation uint64, url string) {
	m.mu.Lock()
	if m.cmd != cmd || m.generation != generation || !m.running || m.url != "" {
		m.mu.Unlock()
		return
	}
	m.url = url
	m.enqueueNotificationLocked(notification{
		generation: generation,
		url:        url,
		cb:         m.onURL,
	})
	m.mu.Unlock()
}

// waitExit reaps the process and flips state back to stopped when it exits.
func (m *Manager) waitExit(cmd *exec.Cmd, generation uint64) {
	defer m.processWG.Done()
	err := cmd.Wait()
	m.mu.Lock()
	if m.cmd != cmd || m.generation != generation {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.url = ""
	m.generation++
	if err != nil && m.lastErr == "" {
		m.lastErr = "cloudflared exited: " + err.Error()
	}
	m.cmd = nil
	m.cancel = nil
	m.enqueueNotificationLocked(notification{
		generation: m.generation,
		cb:         m.onURL,
	})
	m.mu.Unlock()
}

// Stop terminates the tunnel process. It is a no-op when not running.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	cancel := m.cancel
	m.cancel = nil
	m.cmd = nil
	m.generation++
	m.running = false
	m.url = ""
	m.enqueueNotificationLocked(notification{
		generation: m.generation,
		cb:         m.onURL,
	})
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) enqueueNotificationLocked(event notification) {
	if m.dispatcherClosed {
		return
	}
	m.notifications = append(m.notifications, event)
	if !m.dispatcherStarted {
		m.dispatcherWake = make(chan struct{}, 1)
		m.dispatcherStop = make(chan struct{})
		m.dispatcherDone = make(chan struct{})
		m.dispatcherStarted = true
		go m.runDispatcher()
	}
	select {
	case m.dispatcherWake <- struct{}{}:
	default:
	}
}

func (m *Manager) runDispatcher() {
	defer close(m.dispatcherDone)
	for {
		select {
		case <-m.dispatcherWake:
			m.deliverQueuedNotifications()
		case <-m.dispatcherStop:
			return
		}
	}
}

func (m *Manager) deliverQueuedNotifications() {
	for {
		m.mu.Lock()
		if len(m.notifications) == 0 {
			m.mu.Unlock()
			return
		}
		event := m.notifications[0]
		m.notifications = m.notifications[1:]
		m.mu.Unlock()
		if m.beforeDeliver != nil {
			m.beforeDeliver(event.url)
		}

		m.mu.Lock()
		deliver := event.url == "" ||
			(event.generation == m.generation && m.running && m.url == event.url)
		m.mu.Unlock()
		if deliver && event.cb != nil {
			event.cb(event.url)
		}
	}
}

// Close stops the child process, waits for every child to be reaped, then stops
// the callback dispatcher. It is safe to call repeatedly.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closeStarted {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		return
	}
	m.closeStarted = true
	m.closed = true
	cancel := m.cancel
	if m.running {
		m.cancel = nil
		m.cmd = nil
		m.generation++
		m.running = false
		m.url = ""
		m.enqueueNotificationLocked(notification{
			generation: m.generation,
			cb:         m.onURL,
		})
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	m.processWG.Wait()

	m.mu.Lock()
	var dispatcherDone chan struct{}
	if m.dispatcherStarted && !m.dispatcherClosed {
		m.dispatcherClosed = true
		close(m.dispatcherStop)
		dispatcherDone = m.dispatcherDone
	} else {
		m.dispatcherClosed = true
	}
	m.mu.Unlock()
	if dispatcherDone != nil {
		<-dispatcherDone
	}
	close(m.closeDone)
}

func (m *Manager) statusLocked() Status {
	return Status{
		Installed: m.Installed(),
		Running:   m.running,
		URL:       m.url,
		Err:       m.lastErr,
		StartedAt: m.startedAt,
	}
}
