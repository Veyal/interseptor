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
	"io"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// quickTunnelURL matches the assigned trycloudflare URL in cloudflared's stderr.
var quickTunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Status is a snapshot of the tunnel manager's state.
type Status struct {
	Installed bool   `json:"installed"` // cloudflared is on PATH
	Running   bool   `json:"running"`   // a tunnel process is live
	URL       string `json:"url"`       // public URL (fills in shortly after start)
	Err       string `json:"err"`       // last error (start failure / crash)
	StartedAt int64  `json:"startedAt"` // unix millis
}

// Manager owns at most one cloudflared process at a time.
type Manager struct {
	controlPort func() string // returns the loopback control port to expose

	mu        sync.Mutex
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	running   bool
	url       string
	lastErr   string
	startedAt int64
	onURL     func(string) // notified once the URL is known (and on stop, with "")

	// lookPath / now are injected so tests can stub the binary and clock.
	lookPath func(string) (string, error)
	nowMs    func() int64
}

// New builds a Manager. controlPort returns the loopback port string (e.g. "9966")
// the tunnel should forward to.
func New(controlPort func() string) *Manager {
	return &Manager{
		controlPort: controlPort,
		lookPath:    exec.LookPath,
		nowMs:       func() int64 { return time.Now().UnixMilli() },
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
	cmd := exec.CommandContext(runCtx, bin, "tunnel", "--url", target, "--no-autoupdate")
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
	m.running = true
	m.url = ""
	m.lastErr = ""
	m.startedAt = m.nowMs()
	m.mu.Unlock()

	go m.scanForURL(stderr)
	go m.waitExit(cmd)

	return m.Status(), nil
}

// scanForURL reads cloudflared's stderr, extracting the first trycloudflare URL.
func (m *Manager) scanForURL(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if u := quickTunnelURL.FindString(sc.Text()); u != "" {
			m.mu.Lock()
			if m.url == "" {
				m.url = u
			}
			cb := m.onURL
			url := m.url
			m.mu.Unlock()
			if cb != nil {
				cb(url)
			}
			return
		}
	}
	// Drain the rest so the pipe doesn't block the child on a full buffer.
	_, _ = io.Copy(io.Discard, r)
}

// waitExit reaps the process and flips state back to stopped when it exits.
func (m *Manager) waitExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	m.mu.Lock()
	// Only react if this is still the current process (not a stale one after a
	// restart).
	if m.cmd != cmd {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.url = ""
	if err != nil && m.lastErr == "" {
		m.lastErr = "cloudflared exited: " + err.Error()
	}
	cb := m.onURL
	m.cmd = nil
	m.mu.Unlock()
	if cb != nil {
		cb("")
	}
}

// Stop terminates the tunnel process. It is a no-op when not running.
func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.running = false
	m.url = ""
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
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
