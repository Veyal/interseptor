//go:build darwin

package sysproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

func init() { active = darwinBackend{} }

// darwinBackend configures the macOS system proxy via `networksetup`.
type darwinBackend struct{}

func (darwinBackend) supported() bool { return true }

// darwinProxy is one service's web (or secure web) proxy setting.
type darwinProxy struct {
	enabled      bool
	server, port string
}

// darwinPrior is what a service had before Interseptor pointed it at itself.
type darwinPrior struct{ web, secure darwinProxy }

// darwinPriorEnv carries the recorded prior settings across a project-switch or
// self-update restart: those replace the process image and keep the OS proxy
// on, so a record held only in memory would be gone by the time the new image
// shuts down. A crash loses it, and the next start then turns the proxy off
// (RecoverSystemProxy), as before.
const darwinPriorEnv = "INTERSEPTOR_SYSPROXY_PRIOR"

var (
	darwinMu     sync.Mutex
	darwinPriors map[string]darwinPrior // by network service; nil until loaded
)

type darwinPriorJSON struct {
	WebOn        bool   `json:"webOn,omitempty"`
	WebServer    string `json:"webServer,omitempty"`
	WebPort      string `json:"webPort,omitempty"`
	SecureOn     bool   `json:"secureOn,omitempty"`
	SecureServer string `json:"secureServer,omitempty"`
	SecurePort   string `json:"securePort,omitempty"`
}

// loadDarwinPriorsLocked reads the record inherited from a previous image.
func loadDarwinPriorsLocked() {
	if darwinPriors != nil {
		return
	}
	darwinPriors = map[string]darwinPrior{}
	var saved map[string]darwinPriorJSON
	if json.Unmarshal([]byte(os.Getenv(darwinPriorEnv)), &saved) != nil {
		return
	}
	for svc, p := range saved {
		darwinPriors[svc] = darwinPrior{
			web:    darwinProxy{enabled: p.WebOn, server: p.WebServer, port: p.WebPort},
			secure: darwinProxy{enabled: p.SecureOn, server: p.SecureServer, port: p.SecurePort},
		}
	}
}

func saveDarwinPriorsLocked() {
	if len(darwinPriors) == 0 {
		_ = os.Unsetenv(darwinPriorEnv)
		return
	}
	out := make(map[string]darwinPriorJSON, len(darwinPriors))
	for svc, p := range darwinPriors {
		out[svc] = darwinPriorJSON{
			WebOn: p.web.enabled, WebServer: p.web.server, WebPort: p.web.port,
			SecureOn: p.secure.enabled, SecureServer: p.secure.server, SecurePort: p.secure.port,
		}
	}
	if b, err := json.Marshal(out); err == nil {
		_ = os.Setenv(darwinPriorEnv, string(b))
	}
}

// resetDarwinState forgets every recorded prior setting. Used by tests.
func resetDarwinState() {
	darwinMu.Lock()
	darwinPriors = nil
	_ = os.Unsetenv(darwinPriorEnv)
	darwinMu.Unlock()
}

// parseDarwinProxy reads `networksetup -getwebproxy` / `-getsecurewebproxy`
// output. Only the exact "Enabled:" line counts ("Authenticated Proxy Enabled:"
// is a different setting).
func parseDarwinProxy(out string) darwinProxy {
	var p darwinProxy
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Enabled":
			p.enabled = val == "Yes"
		case "Server":
			p.server = val
		case "Port":
			if val != "0" {
				p.port = val
			}
		}
	}
	return p
}

// readDarwinPrior captures what svc has now, before it is pointed at host:port.
// A setting that already is host:port is a leftover of Interseptor itself (a
// crashed run), not something of the user's to put back, so it is recorded as off.
func readDarwinPrior(svc, host, port string) (darwinPrior, bool) {
	read := func(flag string) (darwinProxy, bool) {
		out, err := networkSetup(flag, svc)
		if err != nil {
			return darwinProxy{}, false
		}
		p := parseDarwinProxy(string(out))
		if p.server == host && p.port == port {
			p.enabled = false
		}
		return p, true
	}
	web, ok1 := read("-getwebproxy")
	secure, ok2 := read("-getsecurewebproxy")
	return darwinPrior{web: web, secure: secure}, ok1 && ok2
}

// enable routes the active network services through host:port (web + secure
// web), first recording what each service had so disable can put it back.
func (darwinBackend) enable(host string, port int) error {
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	darwinMu.Lock()
	defer darwinMu.Unlock()
	loadDarwinPriorsLocked()
	for _, s := range svcs {
		// Only the first enable records: by the second one the service already
		// points at Interseptor.
		if _, recorded := darwinPriors[s]; !recorded {
			if prior, ok := readDarwinPrior(s, host, p); ok {
				darwinPriors[s] = prior
				saveDarwinPriorsLocked()
			}
		}
		for _, args := range [][]string{
			{"-setwebproxy", s, host, p},
			{"-setsecurewebproxy", s, host, p},
			{"-setwebproxystate", s, "on"},
			{"-setsecurewebproxystate", s, "on"},
		} {
			if err := run(args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// restoreDarwinProxy puts one recorded setting back: the server it named (if
// any), then its on/off state. With nothing recorded it only turns the proxy off.
func restoreDarwinProxy(svc, setFlag, stateFlag string, prior darwinProxy) {
	state := "off"
	if prior.server != "" {
		port := prior.port
		if port == "" {
			port = "0"
		}
		_ = run(setFlag, svc, prior.server, port)
		if prior.enabled {
			state = "on"
		}
	}
	_ = run(stateFlag, svc, state)
}

// disable undoes enable on the active services: a proxy the user had configured
// before is put back, anything else is turned off.
func (darwinBackend) disable() error {
	svcs, err := activeServices()
	if err != nil {
		return err
	}
	darwinMu.Lock()
	defer darwinMu.Unlock()
	loadDarwinPriorsLocked()
	for _, s := range svcs {
		prior := darwinPriors[s] // zero value: turn both off
		restoreDarwinProxy(s, "-setwebproxy", "-setwebproxystate", prior.web)
		restoreDarwinProxy(s, "-setsecurewebproxy", "-setsecurewebproxystate", prior.secure)
	}
	darwinPriors = map[string]darwinPrior{}
	saveDarwinPriorsLocked()
	return nil
}

// status reports whether the system web proxy is currently on (best-effort: the
// first active service).
func (darwinBackend) status() (bool, error) {
	svcs, err := activeServices()
	if err != nil || len(svcs) == 0 {
		return false, err
	}
	out, err := networkSetup("-getwebproxy", svcs[0])
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "Enabled: Yes"), nil
}

// networkSetup is the runner for networksetup commands. It is a package-level
// variable so tests can inject a stub without changing external behaviour.
var networkSetup = func(args ...string) ([]byte, error) {
	return runCommand("networksetup", args...)
}

func run(args ...string) error {
	out, err := networkSetup(args...)
	if err != nil {
		return fmt.Errorf("networksetup %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func activeServices() ([]string, error) {
	out, err := networkSetup("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	return parseServices(string(out)), nil
}

// parseServices extracts the enabled network services from
// `networksetup -listallnetworkservices` output (skipping the header and any
// service prefixed with "*", which marks it disabled).
func parseServices(output string) []string {
	var svcs []string
	for i, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 || strings.TrimSpace(line) == "" { // first line is a header
			continue
		}
		if strings.HasPrefix(line, "*") { // disabled
			continue
		}
		svcs = append(svcs, line)
	}
	return svcs
}
