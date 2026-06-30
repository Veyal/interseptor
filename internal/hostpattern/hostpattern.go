// Package hostpattern matches host strings against simple scope-style patterns
// (exact, *.wildcard). Regex host rules live in internal/scope.
package hostpattern

import "strings"

// MatchHost reports whether host matches pattern. An empty pattern matches every
// host. "*.acme.com" matches acme.com and every subdomain; otherwise the match
// is exact (case-insensitive).
func MatchHost(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)
	if strings.HasPrefix(pattern, "*.") {
		base := pattern[2:]
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}
