package redact

import (
	"sort"
	"strings"
	"sync"
)

// MinSecretLen is the shortest value a Registry will track. Shorter values
// ("1", "on") would mask unrelated text and are not credible secrets.
const MinSecretLen = 6

// Registry is a set of known secret values (variable current values marked
// secret, auth tokens) that Mask removes from any text before it is shown,
// logged, exported or sent to an MCP client. It is safe for concurrent use and
// never returns a registered value.
type Registry struct {
	mu     sync.RWMutex
	values map[string]struct{}
	sorted []string // longest first; rebuilt lazily
	dirty  bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{values: map[string]struct{}{}} }

// Add registers a secret value. Values shorter than MinSecretLen are ignored.
// It reports whether the value was tracked.
func (r *Registry) Add(value string) bool {
	if r == nil || len(value) < MinSecretLen {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.values[value]; !ok {
		r.values[value] = struct{}{}
		r.dirty = true
	}
	return true
}

// Len is the number of tracked values.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values)
}

func (r *Registry) snapshot() []string {
	r.mu.RLock()
	if !r.dirty {
		s := r.sorted
		r.mu.RUnlock()
		return s
	}
	r.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirty {
		s := make([]string, 0, len(r.values))
		for v := range r.values {
			s = append(s, v)
		}
		// Longest first so a secret that contains another is masked whole.
		sort.Slice(s, func(i, j int) bool {
			if len(s[i]) != len(s[j]) {
				return len(s[i]) > len(s[j])
			}
			return s[i] < s[j]
		})
		r.sorted, r.dirty = s, false
	}
	return r.sorted
}

// Mask replaces every registered secret in s with its length+digest
// placeholder, so equal secrets stay comparable without being shown.
func (r *Registry) Mask(s string) string {
	if r == nil || s == "" {
		return s
	}
	for _, v := range r.snapshot() {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, mask(v))
		}
	}
	return s
}

// Contains reports whether s includes any registered secret.
func (r *Registry) Contains(s string) bool {
	if r == nil || s == "" {
		return false
	}
	for _, v := range r.snapshot() {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
