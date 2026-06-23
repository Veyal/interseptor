// Package scope evaluates target-scope rules: which flows are "in scope" for an
// engagement. It focuses the history view, the intercept gate, and the scanner
// without affecting capture. Safe for concurrent use and live-reloadable.
package scope

import (
	"strings"
	"sync"

	"github.com/Veyal/interceptor/internal/store"
)

type compiled struct {
	include bool
	host    string
	path    string
	scheme  string
	port    int
}

// Engine holds a compiled, reloadable rule set.
type Engine struct {
	mu      sync.RWMutex
	rules   []compiled
	hasIncl bool
}

// New returns an empty engine (everything in scope until rules are set).
func New() *Engine { return &Engine{} }

// SetRules recompiles the active (enabled) rules.
func (e *Engine) SetRules(rules []store.ScopeRule) {
	var c []compiled
	hasIncl := false
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		include := r.Action != "exclude"
		if include {
			hasIncl = true
		}
		c = append(c, compiled{include: include, host: r.Host, path: r.Path, scheme: r.Scheme, port: r.Port})
	}
	e.mu.Lock()
	e.rules, e.hasIncl = c, hasIncl
	e.mu.Unlock()
}

// HasIncludes reports whether any active include rule exists — i.e. whether the
// scope is a real allow-list rather than the default "everything is in scope".
func (e *Engine) HasIncludes() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.hasIncl
}

// InScope reports whether a flow is in scope: it matches no exclude rule, and
// (if any include rules exist) matches at least one include rule.
func (e *Engine) InScope(f *store.Flow) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	included := false
	for _, r := range e.rules {
		if matches(r, f) {
			if r.include {
				included = true
			} else {
				return false // an exclude match always wins
			}
		}
	}
	if !e.hasIncl {
		return true
	}
	return included
}

func matches(r compiled, f *store.Flow) bool {
	return hostMatches(r.host, f.Host) &&
		(r.path == "" || strings.HasPrefix(f.Path, r.path)) &&
		(r.scheme == "" || strings.EqualFold(r.scheme, f.Scheme)) &&
		(r.port == 0 || r.port == f.Port)
}

// hostMatches supports exact and leading-wildcard patterns: "*.acme.com" matches
// acme.com and any subdomain; "" matches any host; otherwise an exact match.
func hostMatches(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if strings.HasPrefix(pattern, "*.") {
		base := pattern[2:]
		return host == base || strings.HasSuffix(host, pattern[1:])
	}
	return host == pattern
}
