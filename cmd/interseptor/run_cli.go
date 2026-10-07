package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collreport"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// `interseptor run` and `interseptor lint` drive a project's collections
// without the UI, the proxy or any listener. They are meant for CI:
//
//	interseptor run <collection> -e <env> --data users.csv -n 3 \
//	    --report junit=out.xml,json=out.json --scope api.example.com
//	interseptor lint <collection> -e <env> [--strict]
//
// Exit codes (run): 0 pass, 1 test failures, 2 runtime/script/transport/
// unresolved-variable errors, 3 scripts not approved, 4 scope-blocked,
// 5 import/lint errors (also: bad usage).
//
// Safety defaults: sends are scope-blocked (a declared scope is required, from
// the project or --scope), scripts that are not trusted never run and stop the
// run before any send, variable writes are discarded unless --persist.
func init() {
	if len(os.Args) > 1 && (os.Args[1] == "run" || os.Args[1] == "lint") {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runCollectionCLI(ctx, os.Args[1], os.Args[2:], os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	}
}

const (
	cliUsageRun = `Usage: interseptor run <collection> [options]

  <collection>             collection name or uid
  -e, --env NAME|UID       environment to use
  -d, --data FILE          iteration data (.csv or .json)
  -n, --iterations N       iteration count (default: one per data row, else 1)
      --folder NAME|UID    run only this folder
      --bail[=MODE]        stop on first problem: on-failure (default) or on-error
      --delay-request DUR  pause between requests (e.g. 250ms, 2s)
      --rps N              max requests per second
      --env-var K=V        set a variable for the whole run (repeatable)
      --scope HOSTS        extra in-scope hosts for this run (comma separated)
      --no-scripts         do not run any script
      --allow-scripts      run scripts whose hash is pinned with --trust-hash
      --trust-hash SHA     trust this exact script hash for this run only (repeatable)
      --identity NAME      identity label recorded on every request
      --persist            store script variable writes (default: discard)
      --report SPEC        cli | json=FILE | junit=FILE | html=FILE, comma separated
      --out DIR            directory for report formats given without =FILE
      --project NAME|DIR   project (default: the last used project)
      --data-dir DIR       global data directory (default ~/.interseptor)

Exit codes: 0 pass, 1 test failures, 2 runtime errors, 3 scripts not approved,
4 scope-blocked, 5 import/lint errors or bad usage.
`
	cliUsageLint = `Usage: interseptor lint <collection> [options]

  -e, --env NAME|UID   environment whose variables count as defined
  -d, --data FILE      iteration data whose columns count as defined
      --env-var K=V    treat K as defined (repeatable)
      --strict         warnings (unapproved scripts) fail the gate too
      --json           machine-readable output
      --project NAME|DIR, --data-dir DIR   as for run

Exit codes: 0 clean, 5 lint errors.
`
)

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// bailFlag accepts "--bail" (on-failure) and "--bail=MODE".
type bailFlag string

func (b *bailFlag) String() string   { return string(*b) }
func (b *bailFlag) IsBoolFlag() bool { return true }
func (b *bailFlag) Set(v string) error {
	switch v {
	case "true", "":
		*b = collrun.BailOnFailure
	case "false":
		*b = collrun.BailNone
	case collrun.BailNone, collrun.BailOnFailure, collrun.BailOnError:
		*b = bailFlag(v)
	default:
		return fmt.Errorf("bail must be on-failure or on-error")
	}
	return nil
}

// parseInterspersed parses flags that may follow positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

type cliFlags struct {
	env, dataFile, folder, project, dataDir, report, out, identity, scopeHosts string
	iterations                                                                 int
	rps                                                                        float64
	delay                                                                      string
	bail                                                                       bailFlag
	noScripts, allowScripts, persist, strict, asJSON                           bool
	trust, envVars                                                             stringList
}

func newCLIFlagSet(name string, f *cliFlags, errw io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errw)
	for _, n := range []string{"e", "env"} {
		fs.StringVar(&f.env, n, "", "")
	}
	for _, n := range []string{"d", "data"} {
		fs.StringVar(&f.dataFile, n, "", "")
	}
	for _, n := range []string{"n", "iterations"} {
		fs.IntVar(&f.iterations, n, 0, "")
	}
	fs.StringVar(&f.folder, "folder", "", "")
	fs.StringVar(&f.project, "project", "", "")
	fs.StringVar(&f.dataDir, "data-dir", "", "")
	fs.StringVar(&f.report, "report", "", "")
	fs.StringVar(&f.out, "out", ".", "")
	fs.StringVar(&f.identity, "identity", "", "")
	fs.StringVar(&f.scopeHosts, "scope", "", "")
	fs.StringVar(&f.delay, "delay-request", "", "")
	fs.Float64Var(&f.rps, "rps", 0, "")
	fs.Var(&f.bail, "bail", "")
	fs.BoolVar(&f.noScripts, "no-scripts", false, "")
	fs.BoolVar(&f.allowScripts, "allow-scripts", false, "")
	fs.BoolVar(&f.persist, "persist", false, "")
	fs.BoolVar(&f.strict, "strict", false, "")
	fs.BoolVar(&f.asJSON, "json", false, "")
	fs.Var(&f.trust, "trust-hash", "")
	fs.Var(&f.envVars, "env-var", "")
	return fs
}

// runCollectionCLI is the testable entry point; it returns the exit code.
func runCollectionCLI(ctx context.Context, cmd string, args []string, stdout, stderr io.Writer) int {
	usage := cliUsageRun
	if cmd == "lint" {
		usage = cliUsageLint
	}
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	var f cliFlags
	fs := newCLIFlagSet(cmd, &f, io.Discard)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n\n%s", cmd, err, usage)
		return collrun.ExitImportLint
	}
	if len(pos) != 1 {
		fmt.Fprintf(stderr, "%s: exactly one collection name or uid is required\n\n%s", cmd, usage)
		return collrun.ExitImportLint
	}
	cli := &collCLI{ctx: ctx, f: f, out: stdout, err: stderr}
	if cmd == "lint" {
		return cli.lint(pos[0])
	}
	return cli.run(pos[0])
}

type collCLI struct {
	ctx      context.Context
	f        cliFlags
	out, err io.Writer
	st       *store.Store
}

func (c *collCLI) fail(code int, format string, a ...any) int {
	fmt.Fprintf(c.err, "interseptor: "+format+"\n", a...)
	return code
}

// openProject opens the project store headlessly (no proxy, no listener).
func (c *collCLI) openProject() (func(), error) {
	home, _ := os.UserHomeDir()
	global := strings.TrimSpace(c.f.dataDir)
	if global == "" {
		g, err := dataRoot()
		if err != nil {
			return nil, err
		}
		global = g
	}
	v := strings.TrimSpace(c.f.project)
	if v == "" {
		v = strings.TrimSpace(os.Getenv("INTERSEPTOR_PROJECT"))
	}
	if v == "" {
		v = readLastProject(global)
	}
	dir := global
	if v != "" && !strings.EqualFold(v, "default") {
		_, d := resolveProjectDir(filepath.Join(global, "projects"), v, home)
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("project %q does not exist (looked in %s)", v, d)
		}
		dir = d
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	c.st = st
	return func() { st.Close() }, nil
}

// findCollection resolves a name or uid; an ambiguous name is an error.
func (c *collCLI) findCollection(ref string) (*store.Collection, error) {
	if co, err := c.st.GetCollection(ref); err == nil {
		return co, nil
	}
	all, err := c.st.ListCollections()
	if err != nil {
		return nil, err
	}
	var hit []store.Collection
	for _, co := range all {
		if strings.EqualFold(co.Name, ref) {
			hit = append(hit, co)
		}
	}
	switch len(hit) {
	case 0:
		return nil, fmt.Errorf("collection %q not found", ref)
	case 1:
		return &hit[0], nil
	}
	return nil, fmt.Errorf("collection name %q is ambiguous (%d matches); use its uid", ref, len(hit))
}

func (c *collCLI) findEnv(ref, collUID string) (string, error) {
	if ref == "" {
		return "", nil
	}
	envs, err := c.st.ListEnvironments()
	if err != nil {
		return "", err
	}
	var hit []store.Environment
	for _, e := range envs {
		if e.Kind != "env" || (e.CollectionUID != "" && e.CollectionUID != collUID) {
			continue
		}
		if e.UID == ref {
			return e.UID, nil
		}
		if strings.EqualFold(e.Name, ref) {
			hit = append(hit, e)
		}
	}
	switch len(hit) {
	case 0:
		return "", fmt.Errorf("environment %q not found for this collection", ref)
	case 1:
		return hit[0].UID, nil
	}
	return "", fmt.Errorf("environment name %q is ambiguous; use its uid", ref)
}

func (c *collCLI) findFolder(ref string, items []store.Item) (string, error) {
	if ref == "" {
		return "", nil
	}
	var hit []string
	for _, it := range items {
		if it.Kind != "folder" {
			continue
		}
		if it.UID == ref {
			return it.UID, nil
		}
		if strings.EqualFold(it.Name, ref) {
			hit = append(hit, it.UID)
		}
	}
	switch len(hit) {
	case 0:
		return "", fmt.Errorf("folder %q not found", ref)
	case 1:
		return hit[0], nil
	}
	return "", fmt.Errorf("folder name %q is ambiguous; use its uid", ref)
}

func (c *collCLI) dataset() (*collrun.Dataset, error) {
	if c.f.dataFile == "" {
		return nil, nil
	}
	fh, err := os.Open(c.f.dataFile)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return collrun.ParseData(c.f.dataFile, fh)
}

func (c *collCLI) localVars() (map[string]string, error) {
	if len(c.f.envVars) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range c.f.envVars {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--env-var %q must be KEY=VALUE", kv)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

// reportSpec is one --report entry.
type reportSpec struct{ format, path string }

var reportExt = map[string]string{
	collreport.FormatJSON: "json", collreport.FormatJUnit: "xml", collreport.FormatHTML: "html",
}

// parseReportSpecs parses "cli,json=a.json,junit=b.xml". With no spec the CLI
// summary goes to stdout.
func parseReportSpecs(spec, outDir string) ([]reportSpec, error) {
	if strings.TrimSpace(spec) == "" {
		return []reportSpec{{format: collreport.FormatCLI}}, nil
	}
	var out []reportSpec
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		format, path, _ := strings.Cut(part, "=")
		format = strings.ToLower(strings.TrimSpace(format))
		if format == "xml" {
			format = collreport.FormatJUnit
		}
		ok := false
		for _, f := range collreport.Formats() {
			ok = ok || f == format
		}
		if !ok {
			return nil, fmt.Errorf("unknown report format %q (want cli, json, junit or html)", format)
		}
		path = strings.TrimSpace(path)
		if path == "" && format != collreport.FormatCLI {
			path = filepath.Join(outDir, "report."+reportExt[format])
		} else if path != "" && !filepath.IsAbs(path) && outDir != "." && outDir != "" && filepath.Dir(path) == "." {
			path = filepath.Join(outDir, path)
		}
		out = append(out, reportSpec{format: format, path: path})
	}
	if len(out) == 0 {
		return nil, errors.New("--report is empty")
	}
	return out, nil
}

func parseDelay(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var ms int64
	if _, err := fmt.Sscanf(s, "%d", &ms); err == nil && fmt.Sprint(ms) == s {
		return time.Duration(ms) * time.Millisecond, nil
	}
	return 0, fmt.Errorf("--delay-request %q is not a duration (try 250ms or 2s)", s)
}

// scopeEngine merges the project's scope rules with --scope hosts.
func (c *collCLI) scopeEngine() (*scope.Engine, error) {
	rules, err := c.st.ListScopeRules()
	if err != nil {
		return nil, err
	}
	for _, h := range strings.Split(c.f.scopeHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			rules = append(rules, store.ScopeRule{Enabled: true, Action: "include", Host: h})
		}
	}
	eng := scope.New()
	eng.SetRules(rules)
	return eng, nil
}

func ownListeners(st *store.Store) ([]int, []net.IP) {
	ports := []int{8080, 9966}
	if _, p, err := net.SplitHostPort(resolveControlAddr(st, "")); err == nil {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n > 0 && n != 8080 && n != 9966 {
			ports = append(ports, n)
		}
	}
	var ips []net.IP
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				ips = append(ips, ipn.IP)
			}
		}
	}
	return ports, ips
}

func (c *collCLI) newSender() *sender.Sender {
	snd := sender.New(c.st, capture.New(c.st))
	if v, ok, _ := c.st.GetSetting("upstream.proxyCA"); ok && strings.TrimSpace(v) != "" {
		_ = snd.SetUpstreamProxyCA([]byte(strings.TrimSpace(v)))
	}
	if v, ok, _ := c.st.GetSetting("upstream.proxy"); ok && v != "" {
		_ = snd.SetUpstreamProxy(v)
	}
	return snd
}

func (c *collCLI) run(ref string) int {
	specs, err := parseReportSpecs(c.f.report, c.f.out)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	delay, err := parseDelay(c.f.delay)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	local, err := c.localVars()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	if c.f.allowScripts && len(c.f.trust) == 0 {
		return c.fail(collrun.ExitImportLint, "--allow-scripts needs at least one --trust-hash (run `interseptor lint` to list script hashes)")
	}
	if c.f.noScripts && (c.f.allowScripts || len(c.f.trust) > 0) {
		return c.fail(collrun.ExitImportLint, "--no-scripts cannot be combined with --allow-scripts/--trust-hash")
	}
	if len(c.f.trust) > 0 && !c.f.allowScripts {
		return c.fail(collrun.ExitImportLint, "--trust-hash only takes effect together with --allow-scripts")
	}
	ds, err := c.dataset()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "data file: %v", err)
	}
	closeStore, err := c.openProject()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	defer closeStore()
	coll, err := c.findCollection(ref)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	envUID, err := c.findEnv(c.f.env, coll.UID)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	items, err := c.st.ListItems(coll.UID)
	if err != nil {
		return c.fail(collrun.ExitRuntime, "%v", err)
	}
	folder, err := c.findFolder(c.f.folder, items)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	eng, err := c.scopeEngine()
	if err != nil {
		return c.fail(collrun.ExitRuntime, "%v", err)
	}
	if coll.ScopePolicy != store.ScopePolicyOff && !eng.HasIncludes() {
		return c.fail(collrun.ExitScope, "refusing to send: no scope is declared. Add scope rules to the project or pass --scope host[,host]")
	}

	ports, ips := ownListeners(c.st)
	backend := collrun.NewStoreBackend(collrun.StoreConfig{
		Store: c.st, Sender: c.newSender(), Scope: eng, OwnPorts: ports, OwnIPs: ips,
		PinnedHashes: splitHashes(c.f.trust), Source: collexec.SourceCLI,
	})
	persist := collrun.PersistDiscard
	if c.f.persist {
		persist = collrun.PersistKeep
	}
	bail := string(c.f.bail)
	if bail == "" {
		bail = collrun.BailNone
	}
	runner := collrun.New(backend, c.st)
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-c.ctx.Done():
			runner.Abort()
		case <-stopWatch:
		}
	}()
	rep, err := runner.Run(c.ctx, collrun.Options{
		CollectionUID: coll.UID, FolderUID: folder, EnvUID: envUID,
		Iterations: c.f.iterations, Data: ds, Delay: delay, RPS: c.f.rps, Bail: bail,
		Persist: persist, NoScripts: c.f.noScripts, FailOnQuarantine: !c.f.noScripts,
		Source: collexec.SourceCLI, Identity: c.f.identity, Local: local,
	})
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	return c.emit(rep, backend, specs)
}

func splitHashes(in []string) []string {
	var out []string
	for _, v := range in {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				out = append(out, h)
			}
		}
	}
	return out
}

// emit writes every requested report through the shared scrub pass.
func (c *collCLI) emit(rep *collrun.Report, b collrun.Backend, specs []reportSpec) int {
	opt := collreport.Options{Scrub: b.Scrub}
	for _, s := range specs {
		if s.path == "" {
			if err := collreport.Write(c.out, s.format, rep, opt); err != nil {
				return c.fail(collrun.ExitRuntime, "report %s: %v", s.format, err)
			}
			continue
		}
		data, err := collreport.Render(s.format, rep, opt)
		if err != nil {
			return c.fail(collrun.ExitRuntime, "report %s: %v", s.format, err)
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return c.fail(collrun.ExitRuntime, "report %s: %v", s.format, err)
		}
		if err := os.WriteFile(s.path, data, 0o644); err != nil {
			return c.fail(collrun.ExitRuntime, "report %s: %v", s.format, err)
		}
		fmt.Fprintf(c.err, "interseptor: wrote %s report to %s\n", s.format, s.path)
	}
	return rep.ExitCode()
}

func (c *collCLI) lint(ref string) int {
	ds, err := c.dataset()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "data file: %v", err)
	}
	local, err := c.localVars()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	closeStore, err := c.openProject()
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	defer closeStore()
	coll, err := c.findCollection(ref)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	envUID, err := c.findEnv(c.f.env, coll.UID)
	if err != nil {
		return c.fail(collrun.ExitImportLint, "%v", err)
	}
	backend := collrun.NewStoreBackend(collrun.StoreConfig{Store: c.st, PinnedHashes: splitHashes(c.f.trust)})
	var cols []string
	if ds != nil {
		cols = ds.Columns
	}
	rep, err := collrun.Lint(backend, coll.UID, collrun.LintOptions{EnvUID: envUID, DataColumns: cols, Local: local})
	if err != nil {
		return c.fail(collrun.ExitRuntime, "%v", err)
	}
	for i := range rep.Findings {
		rep.Findings[i].Message = backend.Scrub(rep.Findings[i].Message)
		rep.Findings[i].Path = backend.Scrub(rep.Findings[i].Path)
	}
	if c.f.asJSON {
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printLint(c.out, rep)
	}
	return rep.ExitCode(c.f.strict)
}

func printLint(w io.Writer, rep *collrun.LintReport) {
	fmt.Fprintf(w, "Lint: %s (%d requests, %d scripts)\n", rep.CollectionName, rep.Requests, rep.Scripts)
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "[%s] %s: %s", strings.ToUpper(f.Level), f.Code, f.Message)
		if f.Path != "" {
			fmt.Fprintf(w, "  (%s)", f.Path)
		}
		if f.Hash != "" {
			fmt.Fprintf(w, "  hash %s", f.Hash)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%d errors, %d warnings\n", rep.Errors, rep.Warnings)
}
