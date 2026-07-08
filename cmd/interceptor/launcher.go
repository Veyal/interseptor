package main

import (
	"context"
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

	"github.com/Veyal/interceptor/internal/bind"
	"github.com/Veyal/interceptor/internal/launcher"
	"github.com/Veyal/interceptor/internal/proc"
)

const (
	defaultLauncherAddr  = "127.0.0.1:9965"
	launcherControlStart = 9966
	launcherProxyStart   = 8080
	launcherPortSpan     = 500
)

// launcherAlive/launcherGraceful/launcherForce are indirections over the proc
// package so tests can substitute fakes and assert the launcher's kill path
// calls the PID-reuse-safe AliveInterceptor check rather than the generic
// Alive (which would happily report a recycled, unrelated PID as "our
// process"). Production code always uses the real proc functions below.
var (
	launcherAlive    = proc.AliveInterceptor
	launcherGraceful = proc.Graceful
	launcherForce    = proc.Force
)

// resolveLauncherAddr applies the same loopback-bind policy as the main
// control/proxy listeners (see cmd/interceptor/flags.go's resolveControlAddr
// and internal/bind) to the launcher dashboard's -addr flag: a non-loopback
// bind is only honored when INTERCEPTOR_ALLOW_EXTERNAL_BIND permits it,
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
		log.Printf("launcher addr %q is non-loopback; ignoring (external bind disabled via INTERCEPTOR_ALLOW_EXTERNAL_BIND=0)", addr)
		return defaultLauncherAddr
	}
	return addr
}

// runLauncher runs a small dashboard process that starts/stops per-project
// Interceptor instances (each its own OS process, its own control+proxy
// ports, sharing only the global CA and Starlark checks) and tracks them in
// ~/.interceptor/instances.json. Closing the launcher does NOT stop the
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
	globalDir := filepath.Join(home, ".interceptor")
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

	lh := &launcherServer{
		globalDir:   globalDir,
		projectsDir: filepath.Join(globalDir, "projects"),
		logsDir:     logsDir,
		exe:         exe,
		reg:         reg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", lh.serveDashboard)
	mux.HandleFunc("GET /api/instances", lh.handleList)
	mux.HandleFunc("POST /api/instances/{project}/start", lh.handleStart)
	mux.HandleFunc("POST /api/instances/{project}/stop", lh.handleStop)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("launcher listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("launcher serve: %v", err)
		}
	}()
	log.Printf("Interceptor launcher: dashboard on http://%s", addr)

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

	mu sync.Mutex // serializes check-then-spawn / check-then-stop
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
		MCPEnvHint:  "INTERCEPTOR_CONTROL_URL=http://" + inst.ControlAddr,
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
			out = append(out, runningView(inst))
			delete(running, name)
			continue
		}
		out = append(out, instanceView{Project: name})
	}
	for _, inst := range running {
		out = append(out, runningView(inst))
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
		"INTERCEPTOR_PROXY_ADDR="+proxyAddr,
		"INTERCEPTOR_NO_BROWSER=1",
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
	// moment it exits on its own (crash, `interceptor stop`, etc.) rather than
	// waiting for the next Reconcile to notice a dead pid.
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		_ = lh.reg.Remove(project)
	}()

	writeLauncherJSON(w, http.StatusOK, runningView(inst))
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(launcherDashboardHTML))
}

const launcherDashboardHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>Interceptor — Projects</title>
<style>
  body{font:14px/1.4 -apple-system,Segoe UI,sans-serif;background:#0f1115;color:#e6e6e6;margin:0;padding:32px}
  h1{font-size:18px;margin:0 0 20px}
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:14px}
  .card{background:#181b21;border:1px solid #2a2e37;border-radius:8px;padding:14px}
  .card h2{font-size:15px;margin:0 0 8px;word-break:break-all}
  .pill{display:inline-block;font-size:11px;padding:2px 8px;border-radius:10px;margin-bottom:8px}
  .pill.on{background:#153;color:#7f7}
  .pill.off{background:#333;color:#999}
  .meta{font-size:12px;color:#9aa;margin:2px 0}
  .row{margin-top:10px;display:flex;gap:6px;flex-wrap:wrap}
  button,a.btn{font:inherit;font-size:12px;padding:5px 10px;border-radius:5px;border:1px solid #3a3f4a;background:#232730;color:#e6e6e6;cursor:pointer;text-decoration:none}
  button:hover,a.btn:hover{background:#2c313c}
  input{font:inherit;padding:6px 8px;border-radius:5px;border:1px solid #3a3f4a;background:#181b21;color:#e6e6e6}
  code{background:#0b0d11;padding:2px 5px;border-radius:4px;font-size:11px}
  .new{margin-top:22px;display:flex;gap:8px}
</style></head>
<body>
<h1>Interceptor — running projects</h1>
<div class="grid" id="grid"></div>
<div class="new">
  <input id="newName" placeholder="new project name…">
  <button onclick="startProject(document.getElementById('newName').value); document.getElementById('newName').value=''">+ Start project</button>
</div>
<script>
function esc(s){return String(s).replace(/[&<>"']/g, function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c];});}
async function refresh(){
  const res = await fetch('/api/instances');
  const items = await res.json();
  const grid = document.getElementById('grid');
  grid.innerHTML = items.map(function(it){
    const pill = it.running ? '<span class="pill on">running · pid '+it.pid+'</span>' : '<span class="pill off">stopped</span>';
    const meta = it.running ? (
      '<div class="meta">UI '+esc(it.controlAddr)+'</div>'+
      '<div class="meta">Proxy '+esc(it.proxyAddr)+'</div>'+
      '<div class="meta">MCP <code>'+esc(it.mcpEnvHint)+'</code></div>'
    ) : '';
    const actions = it.running
      ? '<a class="btn" href="'+esc(it.uiUrl)+'" target="_blank">Open</a><button onclick="stopProject(\''+esc(it.project)+'\')">Stop</button>'
      : '<button onclick="startProject(\''+esc(it.project)+'\')">Start</button>';
    return '<div class="card"><h2>'+esc(it.project)+'</h2>'+pill+meta+'<div class="row">'+actions+'</div></div>';
  }).join('');
}
async function startProject(name){
  name = (name || '').trim();
  if(!name) return;
  await fetch('/api/instances/'+encodeURIComponent(name)+'/start', {method:'POST'});
  refresh();
}
async function stopProject(name){
  await fetch('/api/instances/'+encodeURIComponent(name)+'/stop', {method:'POST'});
  refresh();
}
refresh();
setInterval(refresh, 3000);
</script>
</body></html>`
