// Package scope evaluates target-scope rules: which flows are "in scope" for an
// engagement. It focuses the history view, the intercept gate, and the scanner
// without affecting capture. Safe for concurrent use and live-reloadable.
package scope

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/Veyal/interceptor/internal/hostpattern"
	"github.com/Veyal/interceptor/internal/store"
)

type compiled struct {
	include   bool
	hostExact string
	hostWild  string // base domain for *.example.com (without the *.)
	hostRe    *regexp.Regexp
	pathExact string
	pathRe    *regexp.Regexp
	scheme    string
	port      int
}

func (r compiled) hasHostConstraint() bool {
	return r.hostExact != "" || r.hostWild != "" || r.hostRe != nil
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
		hostExact, hostWild, hostRe := compileHostPattern(r.Host)
		pathExact, pathRe := compilePathPattern(r.Path)
		c = append(c, compiled{
			include: include, hostExact: hostExact, hostWild: hostWild, hostRe: hostRe,
			pathExact: pathExact, pathRe: pathRe, scheme: r.Scheme, port: r.Port,
		})
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

// HasActiveRules reports whether any enabled scope rule exists (include or exclude).
func (e *Engine) HasActiveRules() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rules) > 0
}

// HostInScope reports whether a host matches the current rules using host patterns
// only (path/scheme/port on rules are ignored). Used to gate session header
// injection on Repeater/Intruder sends where only the target host is known.
func (e *Engine) HostInScope(host string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	included := false
	for _, r := range e.rules {
		if !r.hasHostConstraint() {
			continue // path/scheme/port-only rules do not gate session by host
		}
		if !hostMatches(r, host) {
			continue
		}
		if r.include {
			included = true
		} else {
			return false
		}
	}
	if !e.hasIncl {
		return true
	}
	return included
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
	return hostMatches(r, f.Host) &&
		pathMatches(r, f.Path) &&
		(r.scheme == "" || strings.EqualFold(r.scheme, f.Scheme)) &&
		(r.port == 0 || r.port == f.Port)
}

func hostMatches(r compiled, host string) bool {
	if !r.hasHostConstraint() {
		return true
	}
	if r.hostRe != nil {
		return r.hostRe.MatchString(strings.ToLower(host))
	}
	pattern := r.hostExact
	if r.hostWild != "" {
		pattern = "*." + r.hostWild
	}
	return hostpattern.MatchHost(pattern, host)
}

func pathMatches(r compiled, path string) bool {
	if r.pathExact == "" && r.pathRe == nil {
		return true
	}
	if r.pathRe != nil {
		return r.pathRe.MatchString(path)
	}
	return strings.HasPrefix(path, r.pathExact)
}

// ValidateRule reports whether host/path patterns compile. Call before persisting
// a scope rule so malformed regex is rejected instead of falling back to literal.
func ValidateRule(r store.ScopeRule) error {
	if err := validateHostPattern(r.Host); err != nil {
		return err
	}
	return validatePathPattern(r.Path)
}

func validateHostPattern(pattern string) error {
	if pattern == "" {
		return nil
	}
	pattern = strings.ToLower(pattern)
	if strings.HasPrefix(pattern, "*.") {
		return nil
	}
	if looksLikeRegex(pattern) {
		if _, err := regexp.Compile("(?i)" + unwrapRegexSlashes(pattern)); err != nil {
			return fmt.Errorf("invalid host regex %q: %w", pattern, err)
		}
	}
	return nil
}

func validatePathPattern(pattern string) error {
	if pattern == "" {
		return nil
	}
	if looksLikeRegex(pattern) {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid path regex %q: %w", pattern, err)
		}
	}
	return nil
}

// compileHostPattern supports exact hosts, leading-wildcard (*.acme.com), and
// regex when the pattern contains regex metacharacters (e.g. .*ohsome.*).
func compileHostPattern(pattern string) (exact, wildBase string, re *regexp.Regexp) {
	if pattern == "" {
		return "", "", nil
	}
	pattern = strings.ToLower(pattern)
	if strings.HasPrefix(pattern, "*.") {
		return "", strings.TrimPrefix(pattern, "*."), nil
	}
	if looksLikeRegex(pattern) {
		if r, err := regexp.Compile("(?i)" + unwrapRegexSlashes(pattern)); err == nil {
			return "", "", r
		}
		// Invalid regex should have been rejected by ValidateRule; treat as exact.
	}
	return pattern, "", nil
}

func compilePathPattern(pattern string) (exact string, re *regexp.Regexp) {
	if pattern == "" {
		return "", nil
	}
	if looksLikeRegex(pattern) {
		if r, err := regexp.Compile(pattern); err == nil {
			return "", r
		}
	}
	return pattern, nil
}

func looksLikeRegex(s string) bool {
	if len(s) >= 2 && s[0] == '/' && s[len(s)-1] == '/' {
		return true
	}
	for _, lit := range []string{".*", "^", "$", "(", ")", "[", "]", "|", "+", "?", `\d`, `\w`, `\s`} {
		if strings.Contains(s, lit) {
			return true
		}
	}
	return false
}

func unwrapRegexSlashes(s string) string {
	if len(s) >= 2 && s[0] == '/' && s[len(s)-1] == '/' {
		return s[1 : len(s)-1]
	}
	return s
}
