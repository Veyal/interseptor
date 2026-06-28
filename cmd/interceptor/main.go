// Command interceptor runs the Interceptor intercepting proxy: an HTTP/HTTPS
// forward proxy plus a localhost control plane that serves the web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/control"
	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/mcp"
	"github.com/Veyal/interceptor/internal/proxy"
	"github.com/Veyal/interceptor/internal/scope"
	"github.com/Veyal/interceptor/internal/store"
	"github.com/Veyal/interceptor/internal/tlsca"
	"github.com/Veyal/interceptor/internal/version"
)

const controlAddr = "127.0.0.1:9966"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Println("interceptor v" + version.String())
			return
		case "update":
			if err := runUpdate(os.Args[2:]); err != nil {
				if errors.Is(err, version.ErrRestartRequired) {
					os.Exit(0)
				}
				fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}
	// `interceptor mcp` runs a Model Context Protocol server on stdio that drives
	// a separately-running interceptor via its control API. All protocol traffic
	// is on stdout; logs go to stderr so they can't corrupt the JSON-RPC stream.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		base := os.Getenv("INTERCEPTOR_CONTROL_URL")
		if base == "" {
			base = "http://" + controlAddr
		}
		if err := mcp.New(base).Serve(os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	globalDir := filepath.Join(home, ".interceptor")

	// --project <name|path> (or INTERCEPTOR_PROJECT) skips the startup prompt.
	fs := flag.NewFlagSet("interceptor", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name or directory (skips the startup picker)")
	openFlag := fs.Bool("open", false, "open the UI in your browser on start (default: don't)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	projectArg := *projectFlag
	if projectArg == "" {
		projectArg = os.Getenv("INTERCEPTOR_PROJECT")
	}
	// No terminal prompt: selecting/creating/switching projects all happen in the
	// web UI (top-bar project badge). A plain launch resumes the last project the
	// UI switched to; --project still forces a specific one for scripts/CI.
	projectName, dir, err := selectProject(globalDir, projectArg, home)
	if err != nil {
		return fmt.Errorf("select project: %w", err)
	}

	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	// Remember the active project so the next plain launch resumes it. Only bare
	// names (and "default") are remembered — an explicit --project /path is a
	// one-off and must not clobber the UI-selected project.
	if projectArg == "" || isBareProjectName(projectArg) {
		writeLastProject(globalDir, projectName)
	}

	// The CA is global (lives outside any project) so trusting it once covers
	// every project — switching projects never means re-installing a cert.
	ca, err := tlsca.LoadOrCreate(filepath.Join(globalDir, "ca"))
	if err != nil {
		return fmt.Errorf("certificate authority: %w", err)
	}

	eng := intercept.New()
	if v, ok, _ := st.GetSetting("intercept.enabled"); ok && v == "1" {
		eng.SetEnabled(true)
	}
	if v, ok, _ := st.GetSetting("intercept.filter.enabled"); ok && v == "1" {
		target, _, _ := st.GetSetting("intercept.filter.target")
		pattern, _, _ := st.GetSetting("intercept.filter.pattern")
		if err := eng.SetInterceptFilter(true, target, pattern); err != nil {
			log.Printf("intercept filter (saved) ignored: %v", err)
		}
	}

	// Wiring cycle: the proxy needs the hub (events), the hub needs the proxy
	// manager (rebind), the manager needs the proxy handler. Create the manager
	// first, then the hub, then the proxy handler, then attach it to the manager.
	sc := scope.New() // one shared target-scope matcher: control owns CRUD, the proxy gate reads it
	pm := &proxyManager{}
	hub := control.New(st, eng, ca, pm, sc)
	// User-authored Starlark scanner checks are global (shared across projects).
	checksDir := filepath.Join(globalDir, "checks")
	migrateGlobalChecks(globalDir, filepath.Join(globalDir, "projects"))
	_ = os.MkdirAll(checksDir, 0o755)
	hub.ChecksDir = checksDir
	hub.SelfAddr = controlAddr // so the active scanner never targets our own API
	hub.ProjectName = projectName
	hub.ProjectDir = dir
	hub.GlobalDir = globalDir
	// Switching projects re-execs this binary with --project <target>: a clean
	// fresh start on the new project's store/CA, no mid-session store swapping.
	// The mechanism is platform-specific (syscall.Exec on Unix, spawn-and-exit on
	// Windows, which has no syscall.Exec) — see reexec_unix.go / reexec_windows.go.
	hub.SwitchProject = func(target string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return reexecProject(exe, target)
	}
	prx := proxy.New(st, capture.New(st), ca, eng, hub)
	prx.Scope = sc
	hub.Upstream = prx.SetUpstreamProxy
	if v, ok, _ := st.GetSetting("upstream.proxy"); ok && v != "" {
		_ = prx.SetUpstreamProxy(v)
	}
	// Capture policy: persist all traffic, or only in-scope (saves DB space).
	hub.SetCaptureScopeOnly = prx.SetCaptureScopeOnly
	if v, ok, _ := st.GetSetting("capture.scopeOnly"); ok && v == "1" {
		prx.SetCaptureScopeOnly(true)
	}
	// Browser telemetry suppression: on by default; users may disable it in Settings.
	hub.SetSuppressBrowserTelemetry = prx.SetSuppressBrowserTelemetry
	if v, ok, _ := st.GetSetting("capture.suppressBrowserTelemetry"); !ok || v == "1" {
		prx.SetSuppressBrowserTelemetry(true)
	}
	pm.handler = prx

	proxyAddr := "127.0.0.1:8080"
	if v, ok, _ := st.GetSetting("proxy.addr"); ok && v != "" {
		proxyAddr = v
	}
	// Never record traffic aimed at our own listeners, so proxying localhost
	// doesn't fill history with — or feedback-loop on — our own UI/API.
	prx.SelfPorts = selfPorts(controlAddr, proxyAddr)
	if err := pm.Start(proxyAddr); err != nil {
		return fmt.Errorf("proxy listen on %s: %w", proxyAddr, err)
	}

	ctrlLn, err := listenRetry(controlAddr)
	if err != nil {
		return fmt.Errorf("control listen on %s: %w", controlAddr, err)
	}
	ctrlSrv := &http.Server{Handler: hub.Handler()}
	go func() {
		if err := ctrlSrv.Serve(ctrlLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("control serve: %v", err)
		}
	}()

	uiURL := "http://" + controlAddr
	log.Printf("Interceptor v%s · project %q: proxy on %s · UI on %s · data %s", version.String(), projectName, pm.Addr(), uiURL, dir)
	// Quiet, daemon-style start by default: only open the browser when the operator
	// opts in (--open / INTERCEPTOR_OPEN_BROWSER), so restarts and headless runs
	// don't pop a new tab. The UI URL is logged above to open yourself.
	if *openFlag || os.Getenv("INTERCEPTOR_OPEN_BROWSER") != "" {
		openBrowser(uiURL)
	}

	// Best-effort update check (every run). Non-blocking; silent on failure;
	// opt out with INTERCEPTOR_NO_UPDATE_CHECK. Result is also served at /api/version.
	if os.Getenv("INTERCEPTOR_NO_UPDATE_CHECK") == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			latest, newer, err := version.CheckLatest(ctx)
			if err != nil || latest == "" {
				return
			}
			hub.SetUpdate(latest, newer)
			if newer {
				log.Printf("↑ A new version is available: v%s (you have v%s) — run `interceptor update` or see https://github.com/%s/releases", latest, version.String(), version.Repo)
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctrlSrv.Shutdown(ctx)
	pm.Shutdown(ctx)
	return nil
}

// proxyManager owns the proxy listener and supports runtime rebinding: it opens
// the new listener before tearing down the old one, so a failed rebind leaves
// the running proxy untouched. It implements control.Rebinder.
type proxyManager struct {
	handler http.Handler

	mu   sync.Mutex
	addr string
	srv  *http.Server
}

func (m *proxyManager) serve(ln net.Listener) *http.Server {
	srv := &http.Server{Handler: m.handler}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("proxy serve: %v", err)
		}
	}()
	return srv
}

// Start brings up the initial proxy listener.
func (m *proxyManager) Start(addr string) error {
	ln, err := listenRetry(addr)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.addr, m.srv = addr, m.serve(ln)
	m.mu.Unlock()
	return nil
}

// listenRetry binds addr, retrying briefly when the port is still held by a
// just-exited predecessor. On a Windows project switch the new process is
// spawned by the old one and must wait for it to release the listeners; the old
// process sets INTERCEPTOR_REEXEC so only that handoff pays the retry cost — a
// normal start still fails fast when the port is genuinely taken by something
// else (so the operator gets an immediate, clear "address in use").
func listenRetry(addr string) (net.Listener, error) {
	attempts := 1
	if os.Getenv("INTERCEPTOR_REEXEC") != "" {
		attempts = 60 // ~9s at 150ms — covers the predecessor's shutdown
	}
	var err error
	for i := 0; i < attempts; i++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp", addr); err == nil {
			return ln, nil
		}
		if i < attempts-1 {
			time.Sleep(150 * time.Millisecond)
		}
	}
	return nil, err
}

// Rebind opens a listener on addr and, only if that succeeds, swaps it in and
// gracefully drains the old one. Returns the bind error otherwise (old kept).
func (m *proxyManager) Rebind(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	newSrv := m.serve(ln)
	m.mu.Lock()
	old := m.srv
	m.addr, m.srv = addr, newSrv
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = old.Shutdown(ctx)
	}()
	log.Printf("proxy rebound to %s", addr)
	return nil
}

// Addr reports the current proxy bind address.
func (m *proxyManager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}

// selfPorts extracts the TCP ports from host:port listener addresses, skipping
// any that don't parse. Used to keep our own traffic out of captured history.
func selfPorts(addrs ...string) []int {
	var ports []int
	for _, a := range addrs {
		if _, p, err := net.SplitHostPort(a); err == nil {
			if n, e := strconv.Atoi(p); e == nil {
				ports = append(ports, n)
			}
		}
	}
	return ports
}

// Shutdown gracefully stops the current proxy listener.
func (m *proxyManager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()
	if srv != nil {
		_ = srv.Shutdown(ctx)
	}
}

// openBrowser best-effort opens url in the default browser. Opening is opt-in
// (--open / INTERCEPTOR_OPEN_BROWSER); INTERCEPTOR_NO_BROWSER hard-disables it.
func openBrowser(url string) {
	if os.Getenv("INTERCEPTOR_NO_BROWSER") != "" {
		return
	}
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	if path, err := exec.LookPath(cmd); err == nil {
		_ = exec.Command(path, args...).Start()
	}
}
