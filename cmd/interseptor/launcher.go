package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/launcher"
	"github.com/Veyal/interseptor/internal/proc"
)

const (
	defaultLauncherAddr  = "127.0.0.1:9965"
	launcherControlStart = 9966
	launcherProxyStart   = 8080
	launcherPortSpan     = 500
	// launcherTokenHeader carries the local-operator proof required on every
	// mutating launcher route (start/stop). Read-only routes (GET / and
	// GET /api/instances) stay open since they're loopback-only informational
	// reads with no side effects.
	launcherTokenHeader = "X-Interseptor-Launcher-Token"
	// bindConfirmTimeout bounds how long handleStart waits for a spawned
	// child to actually accept connections on its control port before
	// answering the start request — a short, bounded poll, not a hang.
	bindConfirmTimeout  = 2 * time.Second
	bindConfirmInterval = 50 * time.Millisecond
)

// launcherAlive/launcherGraceful/launcherForce are indirections over the proc
// package so tests can substitute fakes and assert the launcher's kill path
// calls the PID-reuse-safe AliveInterseptor check rather than the generic
// Alive (which would happily report a recycled, unrelated PID as "our
// process"). Production code always uses the real proc functions below.
var (
	launcherAlive    = proc.AliveInterseptor
	launcherGraceful = proc.Graceful
	launcherForce    = proc.Force
)

// resolveLauncherAddr applies the same loopback-bind policy as the main
// control/proxy listeners (see cmd/interseptor/flags.go's resolveControlAddr
// and internal/bind) to the launcher dashboard's -addr flag: a non-loopback
// bind is only honored when INTERSEPTOR_ALLOW_EXTERNAL_BIND permits it,
// otherwise it's ignored in favor of the loopback default.
//
// This matters more here than for the main control plane: serveDashboard
// serves an unauthenticated page (no session/token) that can start/stop
// project instances and reveals each instance's control address, so an
// unintended non-loopback bind would expose that to the LAN/internet with
// no gate at all. Known tradeoff: even a policy-approved external bind
// still has no per-request auth — external bind here is opt-in and the
// operator is assumed to have made that call deliberately, same as for the
// main control/proxy listeners.
func resolveLauncherAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultLauncherAddr
	}
	if !isLoopbackBind(addr) && !bind.ExternalBindAllowed() {
		log.Printf("launcher addr %q is non-loopback; ignoring (external bind disabled via INTERSEPTOR_ALLOW_EXTERNAL_BIND=0)", addr)
		return defaultLauncherAddr
	}
	return addr
}

// runLauncher runs a small dashboard process that starts/stops per-project
// Interseptor instances (each its own OS process, its own control+proxy
// ports, sharing only the global CA and Starlark checks) and tracks them in
// ~/.interseptor/instances.json. Closing the launcher does NOT stop the
// project instances it spawned — they're independent processes so an active
// proxy session survives the dashboard going away.
func runLauncher(args []string) error {
	fs := flag.NewFlagSet("launcher", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addrFlag := fs.String("addr", defaultLauncherAddr, "launcher dashboard listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	addr := resolveLauncherAddr(*addrFlag)

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := migrateDataDir(home); err != nil {
		return fmt.Errorf("migrate data dir: %w", err)
	}
	globalDir := filepath.Join(home, newDataDirName)
	logsDir := filepath.Join(globalDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	reg, err := launcher.Open(filepath.Join(globalDir, "instances.json"))
	if err != nil {
		return fmt.Errorf("open instance registry: %w", err)
	}
	_ = reg.Reconcile(launcherAlive)

	token, err := loadOrCreateLauncherToken(globalDir)
	if err != nil {
		return fmt.Errorf("create launcher token: %w", err)
	}

	lh := &launcherServer{
		globalDir:   globalDir,
		projectsDir: filepath.Join(globalDir, "projects"),
		logsDir:     logsDir,
		exe:         exe,
		reg:         reg,
		token:       token,
		bindHost:    launcherBindHost(addr),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("launcher listen on %s: %w", addr, err)
	}
	srv := newControlServer(lh.routes())
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("launcher serve: %v", err)
		}
	}()
	log.Printf("Interseptor launcher: dashboard on http://%s", addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("launcher shutting down (running project instances are left running)…")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

type launcherServer struct {
	globalDir, projectsDir, logsDir, exe string
	reg                                  *launcher.Registry
	token                                string // required via X-Interseptor-Launcher-Token on mutating routes
	bindHost                             string // explicit non-loopback bind host also accepted as Host

	mu sync.Mutex // serializes check-then-spawn / check-then-stop
}

// routes builds the launcher's HTTP handler. GET / and GET /api/instances
// are loopback-only informational reads and stay open with no credential;
// the mutating start/stop routes require lh.token via requireLauncherToken
// so that no local process (or loopback-reachable web page) can spawn or
// kill a pentest session without proof of local operator intent.
func (lh *launcherServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", lh.serveDashboard)
	mux.HandleFunc("GET /api/instances", lh.handleList)
	mux.Handle("POST /api/instances/{project}/start", lh.requireLauncherToken(http.HandlerFunc(lh.handleStart)))
	mux.Handle("POST /api/instances/{project}/stop", lh.requireLauncherToken(http.HandlerFunc(lh.handleStop)))
	return lh.launcherHostGuard(mux)
}

// requireLauncherToken rejects a mutating request unless it carries the
// exact launcher token via the X-Interseptor-Launcher-Token header. The
// comparison is constant-time to avoid a timing side-channel on an
// otherwise-local secret.
func (lh *launcherServer) requireLauncherToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(launcherTokenHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(lh.token)) != 1 {
			launcherErr(w, http.StatusUnauthorized, "missing or invalid "+launcherTokenHeader+" header — read the token from ~/.interseptor/launcher.token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadOrCreateLauncherToken generates a fresh random token on every launcher
// start and writes it to <globalDir>/launcher.token with 0600 permissions,
// overwriting any previous token so stale tokens never accumulate (an old
// copy of the token becomes worthless the moment a new launcher starts).
func loadOrCreateLauncherToken(globalDir string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate launcher token: %w", err)
	}
	tok := hex.EncodeToString(buf)

	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(globalDir, "launcher.token")
	// Write via a temp file + rename so a crash mid-write never leaves a
	// truncated token on disk, then tighten permissions before the rename
	// target is visible under its final name.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tok, nil
}

type instanceView struct {
	Project     string `json:"project"`
	Running     bool   `json:"running"`
	ControlAddr string `json:"controlAddr,omitempty"`
	ProxyAddr   string `json:"proxyAddr,omitempty"`
	PID         int    `json:"pid,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	UIURL       string `json:"uiUrl,omitempty"`
	MCPURL      string `json:"mcpUrl,omitempty"`
	MCPEnvHint  string `json:"mcpEnvHint,omitempty"`
	Path        string `json:"path,omitempty"`     // project data directory
	Modified    string `json:"modified,omitempty"` // data dir mtime (RFC3339), for recency sorting
}

// projectPath is a project's data directory: "default" is the global root
// itself (see resolveProjectDir), every other name lives under projectsDir.
func (lh *launcherServer) projectPath(name string) string {
	if name == "default" {
		return lh.globalDir
	}
	return filepath.Join(lh.projectsDir, name)
}

// decorate adds the display-only path and last-modified fields.
func (lh *launcherServer) decorate(v instanceView) instanceView {
	v.Path = lh.projectPath(v.Project)
	if fi, err := os.Stat(v.Path); err == nil {
		v.Modified = fi.ModTime().UTC().Format(time.RFC3339)
	}
	return v
}

func runningView(inst launcher.Instance) instanceView {
	return instanceView{
		Project:     inst.Project,
		Running:     true,
		ControlAddr: inst.ControlAddr,
		ProxyAddr:   inst.ProxyAddr,
		PID:         inst.PID,
		StartedAt:   inst.StartedAt,
		UIURL:       "http://" + inst.ControlAddr,
		MCPURL:      "http://" + inst.ControlAddr + "/mcp",
		MCPEnvHint:  "INTERSEPTOR_CONTROL_URL=http://" + inst.ControlAddr,
	}
}

// knownProjects lists "default" plus every saved project directory.
func (lh *launcherServer) knownProjects() []string {
	seen := map[string]bool{"default": true}
	names := []string{"default"}
	for _, n := range listProjects(lh.projectsDir) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// views merges known project directories with live registry state so the
// dashboard shows every project (started or not) plus any registry entry for
// a project whose directory it didn't otherwise find.
func (lh *launcherServer) views() []instanceView {
	_ = lh.reg.Reconcile(launcherAlive)
	running := map[string]launcher.Instance{}
	for _, inst := range lh.reg.All() {
		running[inst.Project] = inst
	}

	var out []instanceView
	for _, name := range lh.knownProjects() {
		if inst, ok := running[name]; ok {
			out = append(out, lh.decorate(runningView(inst)))
			delete(running, name)
			continue
		}
		out = append(out, lh.decorate(instanceView{Project: name}))
	}
	for _, inst := range running {
		out = append(out, lh.decorate(runningView(inst)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

func (lh *launcherServer) handleList(w http.ResponseWriter, r *http.Request) {
	writeLauncherJSON(w, http.StatusOK, lh.views())
}

func (lh *launcherServer) handleStart(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.PathValue("project"))
	if project == "" {
		launcherErr(w, http.StatusBadRequest, "project required")
		return
	}
	if project != "default" {
		if _, err := sanitizeProjectName(project); err != nil {
			launcherErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	lh.mu.Lock()
	defer lh.mu.Unlock()

	_ = lh.reg.Reconcile(launcherAlive)
	if inst, ok := lh.reg.Get(project); ok && launcherAlive(inst.PID) {
		writeLauncherJSON(w, http.StatusOK, runningView(inst))
		return
	}

	controlPort, proxyPort, err := lh.allocatePorts()
	if err != nil {
		launcherErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	logFile, err := os.OpenFile(filepath.Join(lh.logsDir, project+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		launcherErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := exec.Command(lh.exe, "--project", project, "--control-port", strconv.Itoa(controlPort))
	cmd.Env = append(os.Environ(),
		"INTERSEPTOR_PROXY_ADDR="+proxyAddr,
		"INTERSEPTOR_NO_BROWSER=1",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		launcherErr(w, http.StatusInternalServerError, fmt.Sprintf("start %q: %v", project, err))
		return
	}

	inst := launcher.Instance{
		Project:     project,
		ControlAddr: controlAddr,
		ProxyAddr:   proxyAddr,
		PID:         cmd.Process.Pid,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := lh.reg.Upsert(inst); err != nil {
		log.Printf("launcher: registry save failed: %v", err)
	}

	// Reap the child so it doesn't zombie, and drop it from the registry the
	// moment it exits on its own (crash, `interseptor stop`, etc.) rather than
	// waiting for the next Reconcile to notice a dead pid.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		_ = lh.reg.Remove(project)
		close(exited)
	}()

	// Confirm the child actually bound its control port before answering
	// 200 — cmd.Start() only proves the process launched, not that it came
	// up successfully. This is a short, bounded poll: it returns the moment
	// the port answers, and never blocks the success path.
	if !waitForBind(controlAddr, bindConfirmTimeout, exited) {
		launcherErr(w, http.StatusGatewayTimeout, fmt.Sprintf(
			"%q started (pid %d) but its control port %s did not come up within %s — check %s",
			project, inst.PID, controlAddr, bindConfirmTimeout, filepath.Join(lh.logsDir, project+".log")))
		return
	}

	writeLauncherJSON(w, http.StatusOK, runningView(inst))
}

// waitForBind polls addr with short TCP dials until something accepts a
// connection, the exited channel closes (the child died before binding), or
// timeout elapses. It returns as soon as the port answers, so the success
// path is never slower than the child's actual startup time.
func waitForBind(addr string, timeout time.Duration, exited <-chan struct{}) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return false
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, bindConfirmInterval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(bindConfirmInterval)
	}
	return false
}

func (lh *launcherServer) handleStop(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.PathValue("project"))

	lh.mu.Lock()
	inst, ok := lh.reg.Get(project)
	lh.mu.Unlock()
	if !ok || !launcherAlive(inst.PID) {
		launcherErr(w, http.StatusNotFound, "not running")
		return
	}

	_ = launcherGraceful(inst.PID)
	go func(pid int, project string) {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) && launcherAlive(pid) {
			time.Sleep(200 * time.Millisecond)
		}
		if launcherAlive(pid) {
			_ = launcherForce(pid)
		}
		lh.mu.Lock()
		_ = lh.reg.Remove(project)
		lh.mu.Unlock()
	}(inst.PID, project)

	writeLauncherJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

// allocatePorts picks the first free control/proxy port pair, skipping ports
// already claimed by other *live* registry entries in addition to probing an
// actual bind — so two Start calls in quick succession don't race onto the
// same port before either process has bound it.
func (lh *launcherServer) allocatePorts() (controlPort, proxyPort int, err error) {
	used := map[int]bool{}
	for _, inst := range lh.reg.All() {
		if !launcherAlive(inst.PID) {
			continue
		}
		if _, p, e := net.SplitHostPort(inst.ControlAddr); e == nil {
			if n, e2 := strconv.Atoi(p); e2 == nil {
				used[n] = true
			}
		}
		if _, p, e := net.SplitHostPort(inst.ProxyAddr); e == nil {
			if n, e2 := strconv.Atoi(p); e2 == nil {
				used[n] = true
			}
		}
	}
	controlPort, err = launcher.FindFreePort("127.0.0.1", launcherControlStart, launcherPortSpan, used)
	if err != nil {
		return 0, 0, err
	}
	used[controlPort] = true
	proxyPort, err = launcher.FindFreePort("127.0.0.1", launcherProxyStart, launcherPortSpan, used)
	if err != nil {
		return 0, 0, err
	}
	return controlPort, proxyPort, nil
}

func writeLauncherJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func launcherErr(w http.ResponseWriter, status int, msg string) {
	writeLauncherJSON(w, status, map[string]string{"error": msg})
}

func (lh *launcherServer) serveDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// The dashboard page itself is an unauthenticated informational read
	// (loopback-only), but the mutating start/stop buttons it renders need
	// the launcher token to succeed against the now-protected API. Loading
	// this page IS the proof of local operator intent, so it's safe to hand
	// the token to the page's own JS here — a JSON-encoded string literal
	// keeps it safe to embed regardless of what characters hex happens to
	// contain (it never will, but this avoids relying on that).
	tokJSON, _ := json.Marshal(lh.token)
	page := strings.Replace(launcherDashboardHTML, "__LAUNCHER_TOKEN__", string(tokJSON), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

const launcherDashboardHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Interseptor — Projects</title>
<style>
:root{
  color-scheme:light dark;
  --bg:#ffffff;--bg1:#f7f8fa;--bg2:#f0f2f5;--bg3:#e7ebf0;
  --line:rgba(15,23,42,.1);--line2:rgba(15,23,42,.22);
  --fg:#0f172a;--fg2:#334155;--fg3:#475569;
  --accent:#065f46;--accentSolid:#047857;--onAccent:#ffffff;--accentDim:rgba(6,95,70,.12);
  --red:#b91c1c;--redDim:rgba(185,28,28,.08);
  --focus-ring:#7c2d12;--shadow:rgba(15,23,42,.08);
  --mono:ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,monospace;
  --ui:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
  --sp-1:4px;--sp-2:8px;--sp-3:12px;--sp-4:16px;--sp-5:24px;--sp-6:32px;
  --r-sm:3px;--r-md:6px;--r-lg:9px;--r-pill:999px;
}
@media (prefers-color-scheme: dark){
  :root{
    --bg:#111318;--bg1:#171a20;--bg2:#1c2028;--bg3:#252b35;
    --line:rgba(255,255,255,.08);--line2:rgba(255,255,255,.18);
    --fg:#f8fafc;--fg2:#cbd5e1;--fg3:#94a3b8;
    --accent:#6ee7c2;--accentDim:rgba(110,231,194,.14);
    --red:#f87171;--redDim:rgba(239,68,68,.15);
    --focus-ring:#facc15;--shadow:rgba(0,0,0,.4);
  }
}
*{box-sizing:border-box}
html{background:var(--bg)}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 var(--ui);font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif}
main{max-width:880px;margin:0 auto;padding:var(--sp-5) var(--sp-4) var(--sp-6)}
header{display:flex;flex-wrap:wrap;align-items:center;gap:var(--sp-3);margin-bottom:var(--sp-4)}
h1{font-size:20px;line-height:1.2;margin:0;font-weight:650}
.count{color:var(--fg2);font-size:13px;white-space:nowrap}
.search{flex:1 1 220px;min-width:0;margin-left:auto;max-width:360px}
.sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
input[type=search],input[type=text]{font:inherit;width:100%;min-height:36px;padding:0 var(--sp-3);background:var(--bg1);color:var(--fg);border:1px solid var(--line2);border-radius:var(--r-md)}
input::placeholder{color:var(--fg3);opacity:1}
input[aria-invalid=true]{border-color:var(--red)}
:focus-visible{outline:3px solid var(--focus-ring);outline-offset:2px}
.btn{display:inline-flex;align-items:center;justify-content:center;min-height:36px;padding:0 var(--sp-3);font:600 13px/1 var(--ui);color:var(--fg);background:var(--bg2);border:1px solid var(--line2);border-radius:var(--r-md);cursor:pointer;text-decoration:none;white-space:nowrap}
.btn:hover{background:var(--bg3)}
.btn[disabled],.btn[aria-disabled=true]{opacity:.6;cursor:progress}
.btn.primary{background:var(--accentSolid);border-color:var(--accentSolid);color:var(--onAccent)}
.btn.primary:hover{filter:brightness(1.08)}
.btn.danger{color:var(--red);background:transparent}
.btn.danger:hover{background:var(--redDim)}
.list{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:var(--sp-2)}
.item{display:flex;flex-wrap:wrap;align-items:center;gap:var(--sp-2) var(--sp-4);padding:var(--sp-3) var(--sp-4);background:var(--bg1);border:1px solid var(--line);border-radius:var(--r-lg)}
.item.on{border-color:var(--accent)}
.info{flex:1 1 260px;min-width:0}
.top{display:flex;align-items:center;flex-wrap:wrap;gap:var(--sp-2)}
.top h2{margin:0;font-size:15px;font-weight:650;overflow-wrap:anywhere}
.pill{display:inline-flex;align-items:center;gap:6px;padding:2px 9px;border-radius:var(--r-pill);font-size:12px;font-weight:600;border:1px solid var(--line2);color:var(--fg2);background:var(--bg2)}
.pill::before{content:"";width:7px;height:7px;border-radius:50%;background:var(--fg3)}
.pill.on{color:var(--accent);background:var(--accentDim);border-color:var(--accent)}
.pill.on::before{background:var(--accent)}
.pill.busy::before{animation:pulse 1s ease-in-out infinite alternate}
@keyframes pulse{from{opacity:.3}to{opacity:1}}
.meta{margin:var(--sp-1) 0 0;font-size:12px;color:var(--fg2);display:flex;flex-wrap:wrap;gap:0 var(--sp-3)}
.meta code,.path{font-family:var(--mono);font-size:12px}
.path{margin:var(--sp-1) 0 0;color:var(--fg3);overflow-wrap:anywhere}
.mcp{display:flex;align-items:flex-start;gap:var(--sp-2);margin-top:var(--sp-2);flex-wrap:wrap}
.mcp code{flex:1 1 200px;min-width:0;font-family:var(--mono);font-size:12px;padding:6px var(--sp-2);background:var(--bg2);border:1px solid var(--line);border-radius:var(--r-md);overflow-wrap:anywhere;word-break:break-all}
.mcplabel{font-size:12px;color:var(--fg2);padding-top:8px}
.btn.small{min-height:44px;min-width:44px}
.actions{display:flex;gap:var(--sp-2);flex:0 0 auto}
.state{padding:var(--sp-6) var(--sp-4);text-align:center;color:var(--fg2);border:1px dashed var(--line2);border-radius:var(--r-lg)}
.state[hidden]{display:none}
.state strong{display:block;color:var(--fg);font-size:15px;margin-bottom:var(--sp-1)}
.state .btn{margin-top:var(--sp-3)}
.banner{display:flex;flex-wrap:wrap;align-items:center;gap:var(--sp-3);margin-bottom:var(--sp-3);padding:var(--sp-2) var(--sp-3);border:1px solid var(--red);border-radius:var(--r-md);background:var(--redDim);color:var(--fg)}
.banner[hidden]{display:none}
.skel{height:64px;border-radius:var(--r-lg);background:var(--bg2);animation:pulse 1.2s ease-in-out infinite alternate}
form.new{margin-top:var(--sp-5);padding:var(--sp-4);background:var(--bg1);border:1px solid var(--line);border-radius:var(--r-lg)}
form.new label{display:block;font-weight:600;margin-bottom:var(--sp-2)}
.row{display:flex;gap:var(--sp-2);flex-wrap:wrap}
.row input{flex:1 1 200px;width:auto}
.hint{margin:var(--sp-2) 0 0;font-size:12px;color:var(--fg3)}
.hint.err{color:var(--red)}
#status{margin:var(--sp-3) 0 0;min-height:1.4em;font-size:13px;color:var(--fg2)}
#status.err{color:var(--red)}
@media (max-width:520px){
  main{padding:var(--sp-4) var(--sp-4) var(--sp-5)}
  .search{flex-basis:100%;max-width:none;margin-left:0;order:3}
  .actions{width:100%}
  .actions .btn{flex:1}
  .btn,input[type=search],input[type=text]{min-height:44px}
}
@media (prefers-reduced-motion: reduce){
  *,*::before,*::after{animation:none!important;transition:none!important}
}
</style></head>
<body>
<main>
<header>
  <h1>Projects</h1>
  <span class="count" id="count" aria-hidden="true"></span>
  <div class="search">
    <label class="sr" for="q">Search projects</label>
    <input id="q" type="search" placeholder="Search projects…" autocomplete="off" spellcheck="false">
  </div>
</header>
<div class="banner" id="error" role="alert" hidden>
  <span id="errorText">Could not reach the launcher.</span>
  <button class="btn" type="button" id="retry">Retry</button>
</div>
<div id="state" class="state" hidden></div>
<ul class="list" id="list" aria-label="Projects"></ul>
<form class="new" id="newForm" novalidate>
  <label for="newName">New project name</label>
  <div class="row">
    <input id="newName" name="project" type="text" autocomplete="off" spellcheck="false" maxlength="64" aria-describedby="newHint" placeholder="e.g. example-app">
    <button class="btn primary" type="submit" id="newBtn">Create and start</button>
  </div>
  <p class="hint" id="newHint">Letters, numbers, dashes and dots. No slashes.</p>
</form>
<p id="status" role="status" aria-live="polite"></p>
</main>
<script>
const LAUNCHER_TOKEN = __LAUNCHER_TOKEN__;
function esc(s){return String(s).replace(/[&<>"']/g, function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c];});}
var items = [], loaded = false, lastSig = '', filter = '', busy = {};
var $ = function(id){return document.getElementById(id);};

function ago(iso){
  var t = Date.parse(iso); if(!t) return '';
  var s = Math.max(0, Math.round((Date.now()-t)/1000));
  if(s < 60) return 'just now';
  if(s < 3600) return Math.floor(s/60)+'m ago';
  if(s < 86400) return Math.floor(s/3600)+'h ago';
  return Math.floor(s/86400)+'d ago';
}
function stamp(it){ return Date.parse(it.running ? it.startedAt : it.modified) || 0; }
function sorted(list){
  return list.slice().sort(function(a,b){
    if(a.running !== b.running) return a.running ? -1 : 1;
    var d = stamp(b) - stamp(a);
    return d || a.project.localeCompare(b.project);
  });
}
function say(msg, isErr){
  var el = $('status'); el.textContent = msg; el.className = isErr ? 'err' : '';
}
function rowHTML(it){
  var name = it.project, b = busy[name];
  var pillCls = it.running ? 'pill on' : 'pill';
  var pillTxt = b ? (b === 'stop' ? 'Stopping…' : 'Starting…') : (it.running ? 'Running' : 'Stopped');
  var meta = '';
  if(it.running){
    meta = '<p class="meta"><span>UI <code>'+esc(it.controlAddr)+'</code></span><span>Proxy <code>'+esc(it.proxyAddr)+'</code></span><span>pid '+esc(it.pid)+'</span>'+
      (it.startedAt ? '<span>started '+esc(ago(it.startedAt))+'</span>' : '')+'</p>'+
      (it.mcpEnvHint ? '<div class="mcp"><span class="mcplabel">MCP</span><code>'+esc(it.mcpEnvHint)+'</code><button class="btn small" type="button" data-action="copy" data-text="'+esc(it.mcpEnvHint)+'" aria-label="Copy MCP setting for '+esc(name)+'">Copy</button></div>' : '');
  } else if(it.modified){
    meta = '<p class="meta"><span>last used '+esc(ago(it.modified))+'</span></p>';
  }
  var path = it.path ? '<p class="path">'+esc(it.path)+'</p>' : '';
  var dis = b ? ' disabled aria-disabled="true"' : '';
  var actions = '';
  if(it.running){
    if(/^https?:\/\//.test(it.uiUrl || '')) actions += '<a class="btn primary" href="'+esc(it.uiUrl)+'" target="_blank" rel="noopener noreferrer" aria-label="Open '+esc(name)+' in a new tab">Open</a>';
    actions += '<button class="btn danger" type="button" data-action="stop" data-project="'+esc(name)+'" aria-label="Stop '+esc(name)+'"'+dis+'>Stop</button>';
  } else {
    actions += '<button class="btn primary" type="button" data-action="start" data-project="'+esc(name)+'" aria-label="Start '+esc(name)+'"'+dis+'>Start</button>';
  }
  return '<li class="item'+(it.running?' on':'')+'"><div class="info"><div class="top"><h2>'+esc(name)+'</h2><span class="'+pillCls+(b?' busy':'')+'">'+pillTxt+'</span></div>'+meta+path+'</div><div class="actions">'+actions+'</div></li>';
}
function render(force){
  var q = filter.trim().toLowerCase();
  var shown = sorted(items).filter(function(it){return !q || it.project.toLowerCase().indexOf(q) >= 0;});
  var sig = JSON.stringify([shown, q, busy, loaded, Math.floor(Date.now()/60000)]);
  var running = items.filter(function(i){return i.running;}).length;
  $('count').textContent = loaded ? items.length+' project'+(items.length===1?'':'s')+' · '+running+' running' : '';
  var st = $('state'), list = $('list');
  if(!force && sig === lastSig) return;
  lastSig = sig;
  var active = document.activeElement, key = active && active.dataset && active.dataset.action ? active.dataset.action+'|'+active.dataset.project : '';
  if(!loaded){
    st.hidden = true; list.innerHTML = '<li class="skel" aria-hidden="true"></li><li class="skel" aria-hidden="true"></li>'; list.setAttribute('aria-busy','true'); return;
  }
  list.removeAttribute('aria-busy');
  if(!shown.length){
    st.hidden = false;
    st.innerHTML = q ? '<strong>No matches</strong>Nothing matches “'+esc(filter.trim())+'”.' : '<strong>No projects yet</strong>Create one below to start an instance.';
  } else { st.hidden = true; }
  list.innerHTML = shown.map(rowHTML).join('');
  if(key){
    var parts = key.split('|'), btns = list.querySelectorAll('button[data-action]');
    for(var i=0;i<btns.length;i++){ if(btns[i].dataset.action===parts[0] && btns[i].dataset.project===parts[1] && !btns[i].disabled){ btns[i].focus({preventScroll:true}); break; } }
  }
}
async function refresh(){
  try{
    var res = await fetch('/api/instances', {cache:'no-store'});
    if(!res.ok) throw new Error('HTTP '+res.status);
    items = await res.json();
    if(!Array.isArray(items)) items = [];
    Object.keys(busy).forEach(function(n){
      var it = items.filter(function(i){return i.project===n;})[0];
      if(it && ((busy[n]==='stop') !== it.running)) delete busy[n];
    });
    loaded = true; $('error').hidden = true;
  }catch(e){
    $('errorText').textContent = 'Could not load projects ('+e.message+'). '+(loaded ? 'Showing the last known list.' : '');
    $('error').hidden = false;
    if(!loaded){ loaded = true; items = []; }
  }
  render(false);
}
async function act(name, verb){
  busy[name] = verb; render(false);
  say((verb==='stop' ? 'Stopping ' : 'Starting ')+name+'…', false);
  try{
    var res = await fetch('/api/instances/'+encodeURIComponent(name)+'/'+verb, {method:'POST', headers:{'X-Interseptor-Launcher-Token': LAUNCHER_TOKEN}});
    var body = {}; try{ body = await res.json(); }catch(_){}
    if(!res.ok) throw new Error(body.error || ('HTTP '+res.status));
    say(name+(verb==='stop' ? ' is stopping.' : ' is running.'), false);
    if(verb==='stop') setTimeout(function(){ if(busy[name]){ delete busy[name]; refresh(); } }, 10000);
  }catch(e){
    delete busy[name];
    say('Could not '+verb+' '+name+': '+e.message, true);
  }
  await refresh();
}
function copyText(btn){
  var text = btn.dataset.text || '';
  var done = function(ok){ say(ok ? 'Copied MCP setting.' : 'Copy failed; select the text manually.', !ok); btn.textContent = ok ? 'Copied' : 'Copy'; setTimeout(function(){ btn.textContent = 'Copy'; }, 1500); };
  if(navigator.clipboard && navigator.clipboard.writeText){ navigator.clipboard.writeText(text).then(function(){done(true);}, function(){done(false);}); return; }
  var ta = document.createElement('textarea'); ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0'; document.body.appendChild(ta); ta.select();
  var ok = false; try{ ok = document.execCommand('copy'); }catch(_){} document.body.removeChild(ta); done(ok);
}
function validName(v){
  v = v.trim();
  if(!v) return 'Enter a project name.';
  if(v === '.' || v === '..' || /[\/\\]/.test(v)) return 'Names cannot be "." or ".." or contain slashes.';
  if(/[\u0000-\u001f]/.test(v)) return 'Names cannot contain control characters.';
  return '';
}
$('newForm').addEventListener('submit', function(ev){
  ev.preventDefault();
  var inp = $('newName'), hint = $('newHint'), err = validName(inp.value);
  if(err){
    inp.setAttribute('aria-invalid','true'); hint.textContent = err; hint.className = 'hint err'; inp.focus(); return;
  }
  inp.removeAttribute('aria-invalid'); hint.textContent = 'Letters, numbers, dashes and dots. No slashes.'; hint.className = 'hint';
  var name = inp.value.trim(); inp.value = '';
  act(name, 'start');
});
$('newName').addEventListener('input', function(){
  this.removeAttribute('aria-invalid'); $('newHint').className = 'hint'; $('newHint').textContent = 'Letters, numbers, dashes and dots. No slashes.';
});
$('q').addEventListener('input', function(){ filter = this.value; render(false); });
$('retry').addEventListener('click', function(){ refresh(); });
$('list').addEventListener('click', function(ev){
  var btn = ev.target.closest('button[data-action]');
  if(!btn || btn.disabled) return;
  if(btn.dataset.action === 'copy'){ copyText(btn); return; }
  act(btn.dataset.project, btn.dataset.action === 'stop' ? 'stop' : 'start');
});
render(true);
refresh();
setInterval(function(){ if(!document.hidden) refresh(); }, 3000);
document.addEventListener('visibilitychange', function(){ if(!document.hidden) refresh(); });
</script>
</body></html>`
