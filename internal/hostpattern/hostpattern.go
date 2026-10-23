// Package hostpattern matches host strings against simple scope-style patterns
// (exact, *.wildcard). Regex host rules live in internal/scope.
package hostpattern

import "strings"

// Normalize lowercases a host and strips trailing dots, so a fully-qualified
// "example.com." is treated as the same host as "example.com".
func Normalize(host string) string {
	return strings.TrimRight(strings.ToLower(host), ".")
}

// MatchHost reports whether host matches pattern. An empty pattern matches every
// host. "*.acme.com" matches acme.com and every subdomain; otherwise the match
// is exact. Both sides are normalized (case-insensitive, trailing dot ignored).
func MatchHost(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	pattern = Normalize(pattern)
	host = Normalize(host)
	if strings.HasPrefix(pattern, "*.") {
		base := pattern[2:]
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}
