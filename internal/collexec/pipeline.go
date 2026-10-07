package collexec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// Source identifies the caller of a Step. It selects the default scope policy
// and is recorded for audit.
type Source string

const (
	SourceUI     Source = "ui"
	SourceRunner Source = "runner"
	SourceCLI    Source = "cli"
	SourceMCP    Source = "mcp"
	SourceScript Source = "script"
)

// DefaultTimeout bounds one exchange when settings do not say otherwise.
const DefaultTimeout = 60 * time.Second

// FlowSender is the sender surface the pipeline needs (*sender.Sender).
type FlowSender interface {
	Send(sender.Request) (*store.Flow, error)
}

// ScopeChecker decides whether a host is in scope (*scope.Engine).
type ScopeChecker interface {
	HostInScope(host string) bool
}

// explicitScope is implemented by scope.Engine: true only when an include rule
// makes scope a real allow-list. Private destinations are reachable only when
// scope is explicit and lists the host.
type explicitScope interface{ HasIncludes() bool }

// FlowCtxWriter persists flow context rows (*store.Store).
type FlowCtxWriter interface {
	PutFlowCtx(store.FlowCtx) error
}

// BodyOpener reads stored bodies (*store.Store).
type BodyOpener interface {
	OpenBody(hash string) (io.ReadCloser, error)
}

// Pipeline is the shared execution pipeline. Build it once with NewPipeline
// and call Step from any number of goroutines; it holds no per-step state.
type Pipeline struct {
	Sender   FlowSender
	Exec     Executor     // nil: scripts are reported as skipped
	Scope    ScopeChecker // nil: everything in scope
	Trust    TrustChecker // nil: every script is untrusted (fail closed)
	Flows    FlowCtxWriter
	Bodies   BodyOpener
	Encoder  BodyEncoder
	Jars     *Jars
	Registry *redact.Registry
	Clock    func() time.Time
	Rand     *varstore.Rand
	// OwnPorts/OwnIPs identify the tool's own listeners (:8080, :9966). Sends
	// to them are refused unconditionally, whatever the scope policy.
	OwnPorts []int
	OwnIPs   []net.IP
}

// NewPipeline fills defaults (jars, registry, clock).
func NewPipeline(p Pipeline) *Pipeline {
	if p.Jars == nil {
		p.Jars = &Jars{}
	}
	if p.Registry == nil {
		p.Registry = redact.NewRegistry()
	}
	if p.Clock == nil {
		p.Clock = time.Now
	}
	return &p
}

// StepInput is everything one Step needs. Variable layers are supplied by the
// caller (current-else-initial values per scope, secrets flagged) so the
// pipeline stays independent of how they are stored.
type StepInput struct {
	Chain  Chain
	Layers []varstore.Layer
	Local  map[string]string

	Source Source
	// AI marks requests that originate from the AI/MCP channel (FlagAI).
	AI bool
	// ScopePolicy overrides the default (block|warn|off) when set.
	ScopePolicy string
	// NoScripts skips every script (CLI --no-scripts).
	NoScripts bool
	// FailOnQuarantine blocks the step when any script is untrusted instead of
	// running the request without it (headless runs exit 3).
	FailOnQuarantine bool

	RunID        string
	Iteration    int
	EnvUID       string
	EnvPin       string // base_target_pin of the active environment
	Identity     string
	ParentFlowID int64
}

// scopePolicy picks the effective policy: explicit override, else interactive
// sends warn and everything else (runner, CLI, scripts, MCP) blocks.
func scopePolicy(in StepInput) string {
	if in.ScopePolicy != "" {
		return in.ScopePolicy
	}
	cp := in.Chain.Collection.ScopePolicy
	if in.Source == SourceUI {
		if cp == store.ScopePolicyOff {
			return cp
		}
		return store.ScopePolicyWarn
	}
	if cp == "" {
		return store.ScopePolicyBlock
	}
	return cp
}

func unresolvedPolicy(s string) varstore.Policy {
	switch strings.ToLower(s) {
	case "literal":
		return varstore.PolicyLiteral
	case "empty":
		return varstore.PolicyEmpty
	}
	return varstore.PolicyBlock
}

func (p *Pipeline) isOwn(host string, port int) bool {
	if !slices.Contains(p.OwnPorts, port) {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	for _, o := range p.OwnIPs {
		if o.Equal(ip) {
			return true
		}
	}
	return false
}

func (p *Pipeline) hostInScope(host string) bool {
	return p.Scope == nil || p.Scope.HostInScope(host)
}

// allowPrivate reports whether private destinations may be dialled for host:
// only when scope is an explicit allow-list that includes it.
func (p *Pipeline) allowPrivate(host string) bool {
	if p.Scope == nil || !p.Scope.HostInScope(host) {
		return false
	}
	if e, ok := p.Scope.(explicitScope); ok {
		return e.HasIncludes()
	}
	return true
}

func (p *Pipeline) guardFor(policy string, host string) *sender.IPGuard {
	g := &sender.IPGuard{BlockPrivate: policy == store.ScopePolicyBlock, OwnPorts: p.OwnPorts, OwnIPs: p.OwnIPs}
	if p.allowPrivate(host) {
		g.AllowHosts = []string{host}
	}
	return g
}

// pinMatches checks a base_target_pin ("host", "host:port" or an origin URL)
// against a request URL.
func pinMatches(pin string, u *url.URL) bool {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return true
	}
	var host, port string
	if strings.Contains(pin, "://") {
		pu, err := url.Parse(pin)
		if err != nil {
			return false
		}
		host, port = pu.Hostname(), pu.Port()
		if port == "" {
			port = defaultPortFor(pu.Scheme)
		}
	} else if h, pt, err := net.SplitHostPort(pin); err == nil {
		host, port = h, pt
	} else {
		host = pin
	}
	if !strings.EqualFold(host, u.Hostname()) {
		return false
	}
	if port == "" {
		return true
	}
	up := u.Port()
	if up == "" {
		up = defaultPortFor(u.Scheme)
	}
	return port == up
}

func defaultPortFor(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func portOf(u *url.URL) int {
	if u.Port() != "" {
		n := 0
		for _, c := range u.Port() {
			n = n*10 + int(c-'0')
		}
		return n
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

var errNoSender = errors.New("collexec: pipeline has no sender")

// ctxOrBackground avoids nil-context panics from sloppy callers.
func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
