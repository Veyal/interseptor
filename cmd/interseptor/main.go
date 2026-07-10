// Command interseptor runs the Interseptor intercepting proxy: an HTTP/HTTPS
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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/control"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/proxy"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
	"github.com/Veyal/interseptor/internal/version"
)

const defaultControlAddr = "127.0.0.1:9966"

// isLoopbackBind reports whether an address (host or host:port) names the
// loopback interface: 127.0.0.0/8, ::1, "localhost", or empty (all-interfaces
// bare ":port" is intentionally NOT loopback).
func isLoopbackBind(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Println("interseptor v" + version.String())
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
		case "stop":
			if err := runStop(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "stop failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "launcher":
			if err := runLauncher(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "launcher failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}
	// `interseptor mcp` runs a Model Context Protocol server on stdio that drives
	// a separately-running interseptor via its control API. All protocol traffic
	// is on stdout; logs go to stderr so they can't corrupt the JSON-RPC stream.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		base := os.Getenv("INTERSEPTOR_CONTROL_URL")
		if base == "" {
			base = "http://" + resolveControlAddr(nil, "")
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
	if err := migrateDataDir(home); err != nil {
		return fmt.Errorf("migrate data dir: %w", err)
	}
	globalDir := filepath.Join(home, newDataDirName)

	// --project <name|path> (or INTERSEPTOR_PROJECT) skips the startup prompt.
	fs := flag.NewFlagSet("interseptor", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name or directory (skips the startup picker)")
	openFlag := fs.Bool("open", false, "open the UI in your browser on start (default: don't)")
	controlPortFlag := fs.Int("control-port", 0, "control UI/API TCP port on loopback (default 9966)")
	controlAddrFlag := fs.String("control-addr", "", "control UI/API listen address host:port (overrides --control-port)")
	if err := fs.Parse(normalizeCLIArgs(os.Args[1:])); err != nil {
		return err
	}
	projectArg := *projectFlag
	if projectArg == "" {
		projectArg = os.Getenv("INTERSEPTOR_PROJECT")
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
	// API keys are global (like the CA) so a remote/Tailscale login survives
	// project switches — each project DB used to have its own empty key table.
	if err := st.AttachGlobalKeys(globalDir); err != nil {
		return fmt.Errorf("global API keys: %w", err)
	}
	// Remember the active project so the next plain launch resumes it. Only bare
	// names (and "default") are remembered — an explicit --project /path is a
	// one-off and must not clobber the UI-selected project.
	if projectArg == "" || isBareProjectName(projectArg) {
		writeLastProject(globalDir, projectName)
	}

	// Control-plane listen address: CLI → env → persisted setting → default.
	controlCLI := strings.TrimSpace(*controlAddrFlag)
	if controlCLI == "" && *controlPortFlag != 0 {
		var err error
		controlCLI, err = controlAddrFromPort(*controlPortFlag)
		if err != nil {
			return err
		}
	}
	controlAddr := resolveControlAddr(st, controlCLI)

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
	cm := &controlManager{}
	hub := control.New(st, eng, ca, pm, sc)
	hub.SetControlRebinder(cm)
	// User-authored Starlark scanner checks are global (shared across projects).
	checksDir := filepath.Join(globalDir, "checks")
	activeChecksDir := filepath.Join(globalDir, "active-checks")
	migrateGlobalChecks(globalDir, filepath.Join(globalDir, "projects"))
	_ = os.MkdirAll(checksDir, 0o755)
	_ = os.MkdirAll(activeChecksDir, 0o755)
	hub.ChecksDir = checksDir
	hub.ActiveChecksDir = activeChecksDir
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
	// Invisible (transparent) proxy mode: off by default; accept origin-form
	// requests from non-proxy-configured clients (e.g. iptables/pf/DNS redirect).
	hub.SetInvisibleProxy = prx.SetInvisibleProxy
	if v, ok, _ := st.GetSetting("proxy.invisibleProxy"); ok && v == "1" {
		prx.SetInvisibleProxy(true)
	}
	// TLS-bypass list: CONNECTs to these hosts are tunneled raw (no MITM) so a
	// pinned-but-unimportant domain keeps working while others stay intercepted.
	hub.SetTLSBypassHosts = prx.SetTLSBypassHosts
	hub.SetAutoBypassOnPinFailure = prx.SetAutoBypassOnPinFailure
	prx.OnBypassAdded = hub.NotifyBypassAdded // persist + refresh UI on auto-bypass
	if v, ok, _ := st.GetSetting("proxy.tlsBypassHosts"); ok && v != "" {
		prx.SetTLSBypassHosts(strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }))
	}
	if v, ok, _ := st.GetSetting("proxy.autoBypassOnPinFailure"); ok && v == "1" {
		prx.SetAutoBypassOnPinFailure(true)
	}
	pm.handler = prx
	cm.handler = hub.Handler()
	hub.SyncSelfPorts = func() {
		addrs := pm.Addrs()
		all := append([]string{cm.Addr()}, addrs...)
		prx.SelfPorts = selfPorts(all...)
		hub.SetSelfAddr(cm.Addr())
	}

	proxyAddrs := control.LoadProxyAddrs(st)
	if v := os.Getenv("INTERSEPTOR_PROXY_ADDR"); v != "" {
		proxyAddrs = []string{v} // env wins (lets you run a second instance / custom port without the UI)
	}
	// Never record traffic aimed at our own listeners, so proxying localhost
	// doesn't fill history with — or feedback-loop on — our own UI/API.
	prx.SelfPorts = selfPorts(append([]string{controlAddr}, proxyAddrs...)...)
	if err := pm.StartAddrs(proxyAddrs); err != nil {
		return fmt.Errorf("proxy listen on %s: %w", strings.Join(proxyAddrs, ", "), err)
	}

	if err := cm.Start(controlAddr); err != nil {
		return fmt.Errorf("control listen on %s: %w", controlAddr, err)
	}
	hub.SetSelfAddr(cm.Addr())

	uiURL := "http://" + cm.Addr()
	log.Printf("Interseptor v%s · project %q: proxy on %s · UI on %s · data %s", version.String(), projectName, pm.Addr(), uiURL, dir)
	// Quiet, daemon-style start by default: only open the browser when the operator
	// opts in (--open / INTERSEPTOR_OPEN_BROWSER), so restarts and headless runs
	// don't pop a new tab. The UI URL is logged above to open yourself.
	if *openFlag || os.Getenv("INTERSEPTOR_OPEN_BROWSER") != "" {
		openBrowser(uiURL)
	}

	// Best-effort update check (every run). Non-blocking; silent on failure;
	// opt out with INTERSEPTOR_NO_UPDATE_CHECK. Result is also served at /api/version.
	if os.Getenv("INTERSEPTOR_NO_UPDATE_CHECK") == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			latest, newer, err := version.CheckLatest(ctx)
			if err != nil || latest == "" {
				return
			}
			hub.SetUpdate(latest, newer)
			if newer {
				log.Printf("↑ A new version is available: v%s (you have v%s) — run `interseptor update` or see https://github.com/%s/releases", latest, version.String(), version.Repo)
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down…")
	hub.StopTunnel() // tear down any Cloudflare quick tunnel child process
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm.Shutdown(ctx)
	pm.Shutdown(ctx)
	return nil
}

// controlManager owns the control-plane listener and supports runtime rebinding.
// It implements control.Rebinder.
type controlManager struct {
	handler http.Handler

	mu   sync.Mutex
	addr string
	srv  *http.Server
}

func (m *controlManager) serve(ln net.Listener) *http.Server {
	srv := &http.Server{Handler: m.handler}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("control serve: %v", err)
		}
	}()
	return srv
}

func (m *controlManager) Start(addr string) error {
	ln, err := listenRetry(addr)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.addr, m.srv = addr, m.serve(ln)
	m.mu.Unlock()
	return nil
}

func (m *controlManager) Rebind(addr string) error {
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
	log.Printf("control UI rebound to %s", addr)
	return nil
}

func (m *controlManager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}

func (m *controlManager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()
	if srv != nil {
		_ = srv.Shutdown(ctx)
	}
}

// proxyManager owns one or more proxy listeners and supports runtime rebinding: it opens
// new listeners before tearing down old ones, so a failed rebind leaves the running
// proxy untouched. It implements control.Rebinder and control.MultiProxyRebinder.
type proxyManager struct {
	handler http.Handler

	mu    sync.Mutex
	addrs []string
	srvs  []*http.Server
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

func (m *proxyManager) listenAll(addrs []string) ([]net.Listener, error) {
	lns := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		ln, err := listenRetry(addr)
		if err != nil {
			for _, open := range lns {
				_ = open.Close()
			}
			return nil, err
		}
		lns = append(lns, ln)
	}
	return lns, nil
}

// StartAddrs brings up the initial proxy listeners.
func (m *proxyManager) StartAddrs(addrs []string) error {
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1:8080"}
	}
	lns, err := m.listenAll(addrs)
	if err != nil {
		return err
	}
	srvs := make([]*http.Server, len(lns))
	for i, ln := range lns {
		srvs[i] = m.serve(ln)
	}
	m.mu.Lock()
	m.addrs, m.srvs = append([]string(nil), addrs...), srvs
	m.mu.Unlock()
	return nil
}

// Start brings up a single proxy listener (backward compatible).
func (m *proxyManager) Start(addr string) error {
	return m.StartAddrs([]string{addr})
}

// Rebind opens a listener on addr and, only if that succeeds, swaps it in and
// gracefully drains the old one. Returns the bind error otherwise (old kept).
func (m *proxyManager) Rebind(addr string) error {
	return m.RebindAddrs([]string{addr})
}

// RebindAddrs reconciles the live proxy listeners to the desired address set:
// listeners already bound to a still-desired address are kept as-is, only newly
// added addresses are bound, and dropped addresses are drained. This is what lets
// a user add a second listener (e.g. :8083 alongside :8080) without the rebind
// re-binding — and failing on — the port the running listener still holds.
func (m *proxyManager) RebindAddrs(addrs []string) error {
	desired := dedupeAddrs(addrs)
	if len(desired) == 0 {
		return fmt.Errorf("at least one proxy listen address required")
	}

	m.mu.Lock()
	cur := make(map[string]*http.Server, len(m.addrs))
	for i, a := range m.addrs {
		cur[a] = m.srvs[i]
	}
	m.mu.Unlock()

	// Only bind addresses not already served (open-before-close). listenAll closes
	// anything it opened if one fails, so a bad new address leaves the live set intact.
	var toAdd []string
	for _, a := range desired {
		if _, ok := cur[a]; !ok {
			toAdd = append(toAdd, a)
		}
	}
	lns, err := m.listenAll(toAdd)
	if err != nil {
		return err
	}
	added := make(map[string]*http.Server, len(lns))
	for i, ln := range lns {
		added[toAdd[i]] = m.serve(ln)
	}

	// Build the new ordered listener set: keep existing servers for retained
	// addresses, slot in the freshly bound ones.
	newSrvs := make([]*http.Server, len(desired))
	for i, a := range desired {
		if s, ok := cur[a]; ok {
			newSrvs[i] = s
		} else {
			newSrvs[i] = added[a]
		}
	}
	// Anything currently bound but no longer desired gets drained.
	var toClose []*http.Server
	keep := make(map[string]struct{}, len(desired))
	for _, a := range desired {
		keep[a] = struct{}{}
	}
	for a, s := range cur {
		if _, ok := keep[a]; !ok {
			toClose = append(toClose, s)
		}
	}

	m.mu.Lock()
	m.addrs, m.srvs = desired, newSrvs
	m.mu.Unlock()

	if len(toClose) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, srv := range toClose {
				if srv != nil {
					_ = srv.Shutdown(ctx)
				}
			}
		}()
	}
	log.Printf("proxy listeners: %s", strings.Join(desired, ", "))
	return nil
}

// dedupeAddrs trims blanks and removes duplicate addresses, preserving order.
func dedupeAddrs(addrs []string) []string {
	seen := make(map[string]struct{}, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

// Addr reports the current proxy bind address(es), comma-joined when multiple.
func (m *proxyManager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.addrs) == 0 {
		return ""
	}
	if len(m.addrs) == 1 {
		return m.addrs[0]
	}
	return strings.Join(m.addrs, ", ")
}

// Addrs reports all active proxy listen addresses.
func (m *proxyManager) Addrs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.addrs))
	copy(out, m.addrs)
	return out
}

// Shutdown gracefully stops all proxy listeners.
func (m *proxyManager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	srvs := m.srvs
	m.mu.Unlock()
	for _, srv := range srvs {
		if srv != nil {
			_ = srv.Shutdown(ctx)
		}
	}
}

// listenRetry binds addr, retrying briefly when the port is still held by a
// just-exited predecessor. On a Windows project switch the new process is
// spawned by the old one and must wait for it to release the listeners; the old
// process sets INTERSEPTOR_REEXEC so only that handoff pays the retry cost — a
// normal start still fails fast when the port is genuinely taken by something
// else (so the operator gets an immediate, clear "address in use").
func listenRetry(addr string) (net.Listener, error) {
	attempts := 1
	if os.Getenv("INTERSEPTOR_REEXEC") != "" {
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

// openBrowser best-effort opens url in the default browser.
// (--open / INTERSEPTOR_OPEN_BROWSER); INTERSEPTOR_NO_BROWSER hard-disables it.
func openBrowser(url string) {
	if os.Getenv("INTERSEPTOR_NO_BROWSER") != "" {
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
