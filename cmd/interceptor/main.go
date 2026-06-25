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
	// Show the Burp-style picker only on a real TTY, and never when a project is
	// preselected or prompting is explicitly suppressed (CI, scripted launches).
	interactive := isInteractive() && os.Getenv("INTERCEPTOR_NO_PROMPT") == ""
	projectName, dir, err := selectProject(os.Stdin, os.Stdout, globalDir, projectArg, home, interactive)
	if errors.Is(err, errQuit) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select project: %w", err)
	}

	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer st.Close()

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
	// User-authored Starlark scanner checks live here (created so it's discoverable).
	checksDir := filepath.Join(dir, "checks")
	_ = os.MkdirAll(checksDir, 0o755)
	hub.ChecksDir = checksDir
	hub.SelfAddr = controlAddr // so the active scanner never targets our own API
	hub.ProjectName = projectName
	hub.ProjectDir = dir
	hub.GlobalDir = globalDir
	// Switching projects re-execs this binary with --project <target>: a clean
	// fresh start on the new project's store/CA, no mid-session store swapping.
	hub.SwitchProject = func(target string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return syscall.Exec(exe, []string{exe, "--project", target}, os.Environ())
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

	ctrlLn, err := net.Listen("tcp", controlAddr)
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
				log.Printf("↑ A new version is available: v%s (you have v%s) — https://github.com/%s/releases", latest, version.String(), version.Repo)
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
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.addr, m.srv = addr, m.serve(ln)
	m.mu.Unlock()
	return nil
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
